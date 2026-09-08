package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBingSyncHandlerReturnsAcceptedForQueuedAndCoalescedRequests(t *testing.T) {
	for _, queued := range []bool{true, false} {
		t.Run(map[bool]string{true: "queued", false: "coalesced"}[queued], func(t *testing.T) {
			jobs := &testJobTrigger{queued: queued}
			handler, err := NewBingSyncHandler(jobs, "Bing archive synchronization")
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sync-bing-images-from-db", nil))
			if response.Code != http.StatusAccepted || response.Body.Len() != 0 {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" || jobs.name != "Bing archive synchronization" {
				t.Fatalf("headers = %#v, triggered job = %q", response.Header(), jobs.name)
			}
		})
	}
}

func TestBingSyncHandlerReturnsInternalErrorWhenTriggerFails(t *testing.T) {
	handler, err := NewBingSyncHandler(&testJobTrigger{err: errors.New("trigger failed")}, "Bing archive synchronization")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sync-bing-images-from-db", nil))
	if response.Code != http.StatusInternalServerError || response.Body.Len() != 0 {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
}

func TestNewBingSyncHandlerValidatesDependencies(t *testing.T) {
	if _, err := NewBingSyncHandler(nil, "job"); err == nil {
		t.Fatal("constructor accepted nil trigger")
	}
	if _, err := NewBingSyncHandler(&testJobTrigger{}, ""); err == nil {
		t.Fatal("constructor accepted empty job name")
	}
}

type testJobTrigger struct {
	queued bool
	err    error
	name   string
}

func (trigger *testJobTrigger) Trigger(name string) (bool, error) {
	trigger.name = name
	return trigger.queued, trigger.err
}
