package services

import (
	"log"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

// Notifier is called when a task is assigned to a user.
// Implementations must not block for long and must not panic.
type Notifier interface {
	NotifyAssigned(task models.Task, assigneeEmail string)
}

// LogNotifier is the default Notifier; it only writes a log line.
type LogNotifier struct{}

// NewLogNotifier creates the default logging notifier
func NewLogNotifier() *LogNotifier {
	return &LogNotifier{}
}

// NotifyAssigned logs the assignment
func (n *LogNotifier) NotifyAssigned(task models.Task, assigneeEmail string) {
	log.Printf("notification: task %s (%q) assigned to %s", task.ID, task.Title, assigneeEmail)
}
