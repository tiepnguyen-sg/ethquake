package observer

import "fmt"

type MultiRecorder struct {
	recorders []Recorder
}

func NewMultiRecorder(recorders ...Recorder) (*MultiRecorder, error) {
	if len(recorders) == 0 {
		return nil, fmt.Errorf("at least one recorder is required")
	}
	copyOfRecorders := append([]Recorder(nil), recorders...)
	for index, recorder := range copyOfRecorders {
		if recorder == nil {
			return nil, fmt.Errorf("recorder %d is nil", index)
		}
	}
	return &MultiRecorder{recorders: copyOfRecorders}, nil
}

func (m *MultiRecorder) RecordSpec(observation SpecObservation) error {
	for index, recorder := range m.recorders {
		if err := recorder.RecordSpec(observation); err != nil {
			return fmt.Errorf("recorder %d: %w", index, err)
		}
	}
	return nil
}

func (m *MultiRecorder) RecordBeacon(observation BeaconObservation) error {
	for index, recorder := range m.recorders {
		if err := recorder.RecordBeacon(observation); err != nil {
			return fmt.Errorf("recorder %d: %w", index, err)
		}
	}
	return nil
}

func (m *MultiRecorder) RecordHeadComparison(comparison HeadComparison) error {
	for index, recorder := range m.recorders {
		if err := recorder.RecordHeadComparison(comparison); err != nil {
			return fmt.Errorf("recorder %d: %w", index, err)
		}
	}
	return nil
}

func (m *MultiRecorder) RecordPollFailure(failure PollFailure) error {
	for index, recorder := range m.recorders {
		if err := recorder.RecordPollFailure(failure); err != nil {
			return fmt.Errorf("recorder %d: %w", index, err)
		}
	}
	return nil
}
