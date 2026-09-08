package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
)

type JobTrigger interface {
	Trigger(string) (bool, error)
}

type BingSyncHandler struct {
	jobs    JobTrigger
	jobName string
}

func NewBingSyncHandler(jobs JobTrigger, jobName string) (*BingSyncHandler, error) {
	if jobs == nil || jobName == "" {
		return nil, errors.New("create Bing synchronization handler: job trigger and name are required")
	}
	return &BingSyncHandler{jobs: jobs, jobName: jobName}, nil
}

func (handler *BingSyncHandler) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	queued, err := handler.jobs.Trigger(handler.jobName)
	if err != nil {
		slog.Error("trigger Bing synchronization", "error", err)
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !queued {
		slog.Info("coalesce Bing synchronization request", "job", handler.jobName)
	}
	writer.WriteHeader(http.StatusAccepted)
}
