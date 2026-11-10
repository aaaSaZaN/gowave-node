package xray

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartResponseProtocolVersion(t *testing.T) {
	for _, header := range []struct {
		value, want string
	}{
		{"", "3.4.2"},
		{"true", "1.0.5"},
		{"false", "3.4.2"},
		{"TRUE", "3.4.2"},
	} {
		for _, tc := range []struct {
			name    string
			started bool
			err     error
		}{
			{name: "started", started: true},
			{name: "failed", err: errors.New("core failed")},
		} {
			t.Run(tc.name+"/"+header.value, func(t *testing.T) {
				coreVersion := "26.7.28"
				controller := &Controller{
					xrayVersion:       &coreVersion,
					nodeVersion:       "1.0.5",
					systemStatsGetter: func() interface{} { return nil },
				}
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/node/xray/start", nil)
				request.Header.Set("X-Better-One", header.value)
				controller.startResponse(recorder, request, tc.started, tc.err)
				if recorder.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200", recorder.Code)
				}
				var body struct {
					Response struct {
						IsStarted       bool
						Version         string
						Error           *string
						NodeInformation struct{ Version string }
					}
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				response := body.Response
				if response.NodeInformation.Version != header.want {
					t.Errorf("node version = %q, want %q", response.NodeInformation.Version, header.want)
				}
				if response.Version != coreVersion || response.IsStarted != tc.started {
					t.Errorf("unexpected core status: %+v", response)
				}
				if tc.err == nil {
					if response.Error != nil {
						t.Errorf("error = %q, want null", *response.Error)
					}
				} else if response.Error == nil || *response.Error != tc.err.Error() {
					t.Errorf("error = %v, want %q", response.Error, tc.err)
				}
			})
		}
	}
}
