package xray

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestCloneConfigDeepCopy(t *testing.T) {
	if cloneConfig(nil) != nil {
		t.Fatal("expected nil for nil input")
	}

	orig := map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": "warning",
		},
		"inbounds": []interface{}{
			map[string]interface{}{
				"tag":  "in-1",
				"port": float64(1080),
				"clients": []interface{}{
					map[string]interface{}{"id": "u-1"},
				},
			},
		},
		"tags": []string{"a", "b"},
		"dict": map[string]string{"k": "v"},
	}

	clone := cloneConfig(orig)

	// Check serialization equality.
	origB, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	cloneB, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	if string(origB) != string(cloneB) {
		t.Fatalf("JSON mismatch:\norig:  %s\nclone: %s", origB, cloneB)
	}

	clone["tags"].([]string)[0] = "changed"
	clone["dict"].(map[string]string)["k"] = "changed"
	if orig["tags"].([]string)[0] != "a" || orig["dict"].(map[string]string)["k"] != "v" {
		t.Fatal("original typed collections mutated through clone")
	}
	// Mutate clone and ensure original is untouched.
	clone["log"].(map[string]interface{})["loglevel"] = "debug"
	if orig["log"].(map[string]interface{})["loglevel"] != "warning" {
		t.Fatal("original map mutated through clone")
	}

	inbounds := clone["inbounds"].([]interface{})
	inbounds[0].(map[string]interface{})["tag"] = "in-mutated"
	if orig["inbounds"].([]interface{})[0].(map[string]interface{})["tag"] != "in-1" {
		t.Fatal("original nested inbound mutated through clone")
	}

	clients := inbounds[0].(map[string]interface{})["clients"].([]interface{})
	clients[0].(map[string]interface{})["id"] = "u-mutated"
	if orig["inbounds"].([]interface{})[0].(map[string]interface{})["clients"].([]interface{})[0].(map[string]interface{})["id"] != "u-1" {
		t.Fatal("original deep nested client mutated through clone")
	}
}

func TestCloneConfigNilCollections(t *testing.T) {
	original := map[string]interface{}{
		"slice":   []interface{}(nil),
		"strings": []string(nil),
		"map":     map[string]interface{}(nil),
		"dict":    map[string]string(nil),
	}
	got, err := json.Marshal(cloneConfig(original))
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCommitActivationBackupCleanup(t *testing.T) {
	for _, activated := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "core")
		if err := os.WriteFile(path+".previous", []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
		process := &ProcessManager{execPath: path, activated: activated}
		process.CommitActivation()
		if process.activated {
			t.Fatal("activation remains pending")
		}
		_, err := os.Stat(path + ".previous")
		if activated && !os.IsNotExist(err) {
			t.Fatalf("committed backup remains: %v", err)
		}
		if !activated && err != nil {
			t.Fatalf("unrelated backup removed: %v", err)
		}
	}
}
