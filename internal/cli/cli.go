// Package cli composes Ethquake commands without owning measurement logic.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/execution"
	"github.com/tiepnguyen-sg/ethquake/internal/observer"
	"github.com/tiepnguyen-sg/ethquake/internal/telemetry"
	"github.com/tiepnguyen-sg/ethquake/internal/timeseries"
	"golang.org/x/sync/errgroup"
)

const observeUsage = `Usage:
  ethquake observe --beacon NAME=URL [--beacon NAME=URL ...] [options]

Options:
  --beacon NAME=URL          Named Beacon API endpoint; repeat for each node.
  --execution NAME=URL       Named execution newHeads WebSocket; repeat for each node.
  --metrics-address ADDRESS  Loopback address for Prometheus metrics (default 127.0.0.1:9464).
  --output PATH              JSON Lines output path, or - for stdout (default -).
  --poll-interval DURATION   Beacon polling interval (default 2s).
  --request-timeout DURATION Per-request timeout (default 5s).
  --head-history-limit N     Maximum Beacon slots retained for comparison (default 256).
  --reorg-history-limit N    Maximum canonical execution heads retained (default 256).
`

var targetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

type endpointValue struct {
	name string
	url  string
}

type endpointValues []endpointValue

func (values *endpointValues) String() string {
	return ""
}

func (values *endpointValues) Set(raw string) error {
	name, endpoint, found := strings.Cut(raw, "=")
	if !found || name == "" || endpoint == "" {
		return errors.New("endpoint must use NAME=URL format")
	}
	if !targetNamePattern.MatchString(name) {
		return fmt.Errorf("endpoint name %q is invalid", name)
	}
	*values = append(*values, endpointValue{name: name, url: endpoint})
	return nil
}

func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	if len(arguments) == 0 {
		return errors.New("a command is required\n\n" + observeUsage)
	}

	switch arguments[0] {
	case "observe":
		return runObserve(ctx, arguments[1:], stdout, stderr)
	case "help", "-h", "--help":
		_, err := io.WriteString(stdout, observeUsage)
		return err
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func runObserve(ctx context.Context, arguments []string, stdout, stderr io.Writer) (returnErr error) {
	flags := flag.NewFlagSet("observe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var beaconEndpoints endpointValues
	var executionEndpoints endpointValues
	var metricsAddress string
	var outputPath string
	var pollInterval time.Duration
	var requestTimeout time.Duration
	var headHistoryLimit int
	var reorgHistoryLimit int
	flags.Var(&beaconEndpoints, "beacon", "named Beacon API endpoint")
	flags.Var(&executionEndpoints, "execution", "named execution newHeads WebSocket endpoint")
	flags.StringVar(&metricsAddress, "metrics-address", "127.0.0.1:9464", "loopback Prometheus listen address")
	flags.StringVar(&outputPath, "output", "-", "JSON Lines output path or - for stdout")
	flags.DurationVar(&pollInterval, "poll-interval", 2*time.Second, "Beacon polling interval")
	flags.DurationVar(&requestTimeout, "request-timeout", 5*time.Second, "per-request timeout")
	flags.IntVar(&headHistoryLimit, "head-history-limit", 256, "maximum Beacon slots retained for comparison")
	flags.IntVar(&reorgHistoryLimit, "reorg-history-limit", 256, "maximum canonical execution heads retained")
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, observeUsage)
	}
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse observe flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if len(beaconEndpoints) == 0 {
		return errors.New("at least one --beacon NAME=URL endpoint is required")
	}
	if pollInterval <= 0 {
		return errors.New("--poll-interval must be positive")
	}
	if requestTimeout <= 0 {
		return errors.New("--request-timeout must be positive")
	}
	if reorgHistoryLimit < 2 {
		return errors.New("--reorg-history-limit must be at least 2")
	}
	if headHistoryLimit < 2 {
		return errors.New("--head-history-limit must be at least 2")
	}
	if err := validateLoopbackAddress(metricsAddress); err != nil {
		return fmt.Errorf("validate --metrics-address: %w", err)
	}

	targets := make([]observer.Target, 0, len(beaconEndpoints))
	seenNames := make(map[string]struct{}, len(beaconEndpoints))
	for _, endpoint := range beaconEndpoints {
		if _, exists := seenNames[endpoint.name]; exists {
			return fmt.Errorf("Beacon endpoint name %q is duplicated", endpoint.name)
		}
		seenNames[endpoint.name] = struct{}{}
		client, err := beacon.NewClient(endpoint.url, requestTimeout)
		if err != nil {
			return fmt.Errorf("configure Beacon endpoint %q: %w", endpoint.name, err)
		}
		targets = append(targets, observer.Target{Name: endpoint.name, Beacon: client})
	}

	executionTargets := make([]observer.ExecutionTarget, 0, len(executionEndpoints))
	seenExecutionNames := make(map[string]struct{}, len(executionEndpoints))
	for _, endpoint := range executionEndpoints {
		if _, exists := seenExecutionNames[endpoint.name]; exists {
			return fmt.Errorf("execution endpoint name %q is duplicated", endpoint.name)
		}
		seenExecutionNames[endpoint.name] = struct{}{}
		client, err := execution.NewClient(endpoint.url, requestTimeout)
		if err != nil {
			return fmt.Errorf("configure execution endpoint %q: %w", endpoint.name, err)
		}
		executionTargets = append(executionTargets, observer.ExecutionTarget{Name: endpoint.name, Execution: client})
	}

	listener, err := net.Listen("tcp", metricsAddress)
	if err != nil {
		return fmt.Errorf("listen for Prometheus metrics on %s: %w", metricsAddress, err)
	}
	defer listener.Close()

	output, closeOutput, err := openOutput(outputPath, stdout)
	if err != nil {
		return err
	}
	defer func() {
		if err := closeOutput(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close time-series output: %w", err))
		}
	}()

	jsonRecorder, err := timeseries.NewJSONLRecorder(output)
	if err != nil {
		return fmt.Errorf("create time-series recorder: %w", err)
	}
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		return fmt.Errorf("create Observer metrics: %w", err)
	}
	recorder, err := observer.NewMultiRecorder(jsonRecorder, metrics)
	if err != nil {
		return fmt.Errorf("create Observer recorders: %w", err)
	}

	measurementObserver, err := observer.New(targets, recorder, observer.Options{
		PollInterval:     pollInterval,
		HeadHistoryLimit: headHistoryLimit,
	})
	if err != nil {
		return fmt.Errorf("create Observer: %w", err)
	}

	var executionMeasurementObserver *observer.ExecutionObserver
	if len(executionTargets) > 0 {
		executionRecorder, err := observer.NewMultiExecutionRecorder(jsonRecorder, metrics)
		if err != nil {
			return fmt.Errorf("create execution recorders: %w", err)
		}
		executionMeasurementObserver, err = observer.NewExecutionObserver(
			executionTargets,
			executionRecorder,
			observer.ExecutionOptions{
				HistoryLimit:     reorgHistoryLimit,
				ReconnectInitial: time.Second,
				ReconnectMaximum: 30 * time.Second,
			},
		)
		if err != nil {
			return fmt.Errorf("create execution Observer: %w", err)
		}
	}

	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		return serveMetrics(groupContext, listener, metrics.Handler())
	})
	if executionMeasurementObserver != nil {
		group.Go(func() error {
			if err := executionMeasurementObserver.Run(groupContext); err != nil {
				return fmt.Errorf("run execution Observer: %w", err)
			}
			return nil
		})
	}
	group.Go(func() error {
		if err := measurementObserver.Run(groupContext); err != nil {
			return fmt.Errorf("run Observer: %w", err)
		}
		return nil
	})
	if err := group.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func validateLoopbackAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen host %q is not a literal loopback address", host)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return fmt.Errorf("listen port %q is invalid", portText)
	}
	if port > 65535 {
		return fmt.Errorf("listen port %q is invalid", portText)
	}
	return nil
}

func openOutput(path string, stdout io.Writer) (io.Writer, func() error, error) {
	if path == "-" {
		return stdout, func() error { return nil }, nil
	}
	if path == "" {
		return nil, nil, errors.New("--output must not be empty")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("create time-series output %q: %w", path, err)
	}
	return file, func() error {
		return errors.Join(file.Sync(), file.Close())
	}, nil
}

func serveMetrics(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	shutdownResult := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownResult <- server.Shutdown(shutdownContext)
	}()

	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		if shutdownErr := <-shutdownResult; shutdownErr != nil {
			return fmt.Errorf("shut down Prometheus metrics server: %w", shutdownErr)
		}
		return nil
	}
	return fmt.Errorf("serve Prometheus metrics: %w", err)
}
