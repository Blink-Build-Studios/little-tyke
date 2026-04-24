package logging

import (
	log "github.com/sirupsen/logrus"
)

// ServiceLabelsHook injects static labels into every log entry.
type ServiceLabelsHook struct {
	labels map[string]string
}

func (h *ServiceLabelsHook) Levels() []log.Level {
	return log.AllLevels
}

func (h *ServiceLabelsHook) Fire(entry *log.Entry) error {
	for k, v := range h.labels {
		entry.Data[k] = v
	}
	return nil
}

// Setup configures logrus with JSON formatting and service labels.
func Setup(labels map[string]string) {
	log.SetFormatter(&log.JSONFormatter{})
	log.AddHook(&ServiceLabelsHook{labels: labels})
}

// SetLevel sets the logrus log level from a string.
func SetLevel(level string) error {
	lvl, err := log.ParseLevel(level)
	if err != nil {
		return err
	}
	log.SetLevel(lvl)
	return nil
}
