package stats

import (
	"encoding/json"
	"errors"
)

func requireReset(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	raw, ok := fields["reset"]
	if !ok || string(raw) == "null" {
		return errors.New("reset is required and must be a boolean")
	}
	return nil
}
func (r *ResetRequest) UnmarshalJSON(data []byte) error {
	if err := requireReset(data); err != nil {
		return err
	}
	type plain ResetRequest
	return json.Unmarshal(data, (*plain)(r))
}
func (r *InboundStatsRequest) UnmarshalJSON(data []byte) error {
	if err := requireReset(data); err != nil {
		return err
	}
	type plain InboundStatsRequest
	return json.Unmarshal(data, (*plain)(r))
}
func (r *OutboundStatsRequest) UnmarshalJSON(data []byte) error {
	if err := requireReset(data); err != nil {
		return err
	}
	type plain OutboundStatsRequest
	return json.Unmarshal(data, (*plain)(r))
}
