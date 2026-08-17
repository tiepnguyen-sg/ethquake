// Package report renders the versioned experiment analysis contract.
package report

import (
	"bytes"
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/tiepnguyen-sg/ethquake/internal/experiment"
)

func Markdown(analysis experiment.Analysis) []byte {
	var output bytes.Buffer
	fmt.Fprintf(&output, "# Ethquake experiment report: %s\n\n", analysis.ScenarioName)
	fmt.Fprintf(&output, "Generated: %s\n\n", analysis.GeneratedAt.Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(&output, "## Gate outcome\n\n")
	fmt.Fprintf(&output, "- Gate A — VALIDITY: %s — %s\n", strings.ToUpper(analysis.GateA.Outcome), analysis.GateA.Reason)
	fmt.Fprintf(&output, "- Gate B — SURPRISE: %s — %s\n", strings.ToUpper(analysis.GateB.Outcome), analysis.GateB.Reason)
	fmt.Fprintf(&output, "- Gate C — DIFFERENTIATION: %s — %s\n\n", strings.ToUpper(analysis.GateC.Outcome), analysis.GateC.Reason)
	fmt.Fprintf(&output, "Conclusion: `%s`. This report does not classify any observed behavior as a client bug.\n\n", analysis.Conclusion)
	fmt.Fprintf(&output, "## Finalized-epoch progress\n\n")
	fmt.Fprintf(&output, "| Repetition | Control minimum | Fault maximum | Conservative difference |\n")
	fmt.Fprintf(&output, "|---:|---:|---:|---:|\n")
	for _, pair := range analysis.Pairs {
		fmt.Fprintf(&output, "| %d | %d | %d | %d |\n", pair.Repetition, pair.ControlMinimumProgress, pair.FaultMaximumProgress, pair.ConservativeDifference)
	}
	fmt.Fprintf(&output, "\nControl median %.1f (range %d–%d); fault median %.1f (range %d–%d).\n\n",
		analysis.ControlMinimumProgress.Median, analysis.ControlMinimumProgress.Min, analysis.ControlMinimumProgress.Max,
		analysis.FaultMaximumProgress.Median, analysis.FaultMaximumProgress.Min, analysis.FaultMaximumProgress.Max,
	)
	if len(analysis.RecoveryByClient) > 0 {
		fmt.Fprintf(&output, "## Recovery time\n\n")
		fmt.Fprintf(&output, "| CL client | Slots | Median | Range |\n")
		fmt.Fprintf(&output, "|---|---|---:|---:|\n")
		clients := make([]string, 0, len(analysis.RecoveryByClient))
		for client := range analysis.RecoveryByClient {
			clients = append(clients, client)
		}
		sort.Strings(clients)
		for _, client := range clients {
			statistic := analysis.RecoveryByClient[client]
			fmt.Fprintf(&output, "| %s | %s | %.1f | %d–%d |\n", client, formatValues(statistic.Values), statistic.Median, statistic.Min, statistic.Max)
		}
		fmt.Fprintln(&output)
	}
	fmt.Fprintf(&output, "## Evidence\n\n")
	fmt.Fprintf(&output, "The JSON report preserves every run summary, paired effect, realized split, dependency closure, and measurement-completeness flag. The executed scenario and raw time series remain separate checksum-verified artifacts.\n")
	return output.Bytes()
}

func SVG(analysis experiment.Analysis) []byte {
	const width = 960
	const height = 520
	var output bytes.Buffer
	fmt.Fprintf(&output, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="title desc">`, width, height, width, height)
	fmt.Fprintf(&output, `<title id="title">Ethquake finality progress by paired run</title><desc id="desc">Minimum control-target and maximum fault-target finalized epoch progress for three preregistered repetitions.</desc>`)
	fmt.Fprintf(&output, `<rect width="100%%" height="100%%" fill="#ffffff"/><text x="48" y="48" font-family="sans-serif" font-size="24" font-weight="600">%s</text>`, html.EscapeString(analysis.ScenarioName))
	fmt.Fprintf(&output, `<text x="48" y="76" font-family="sans-serif" font-size="14" fill="#444">Conservative finalized-epoch progress during the three-epoch window</text>`)
	maximum := uint64(1)
	for _, pair := range analysis.Pairs {
		if pair.ControlMinimumProgress > maximum {
			maximum = pair.ControlMinimumProgress
		}
		if pair.FaultMaximumProgress > maximum {
			maximum = pair.FaultMaximumProgress
		}
	}
	for index, pair := range analysis.Pairs {
		x := 110 + index*270
		controlHeight := int(float64(pair.ControlMinimumProgress) / float64(maximum) * 300)
		faultHeight := int(float64(pair.FaultMaximumProgress) / float64(maximum) * 300)
		fmt.Fprintf(&output, `<rect x="%d" y="%d" width="72" height="%d" fill="#276ef1"/><rect x="%d" y="%d" width="72" height="%d" fill="#e4572e"/>`, x, 410-controlHeight, controlHeight, x+88, 410-faultHeight, faultHeight)
		fmt.Fprintf(&output, `<text x="%d" y="436" text-anchor="middle" font-family="sans-serif" font-size="14">Pair %d</text>`, x+80, pair.Repetition)
		fmt.Fprintf(&output, `<text x="%d" y="%d" text-anchor="middle" font-family="sans-serif" font-size="14">%d</text>`, x+36, 398-controlHeight, pair.ControlMinimumProgress)
		fmt.Fprintf(&output, `<text x="%d" y="%d" text-anchor="middle" font-family="sans-serif" font-size="14">%d</text>`, x+124, 398-faultHeight, pair.FaultMaximumProgress)
	}
	fmt.Fprintf(&output, `<rect x="250" y="474" width="18" height="18" fill="#276ef1"/><text x="276" y="488" font-family="sans-serif" font-size="14">Control minimum</text><rect x="450" y="474" width="18" height="18" fill="#e4572e"/><text x="476" y="488" font-family="sans-serif" font-size="14">Fault maximum</text></svg>`)
	return output.Bytes()
}

func formatValues(values []uint64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ", ")
}
