package domain

import (
	"strings"
	"time"
)

const maxHandoverEntries = 1000

// HandoverEntry is one shift-handover record attached to a run. The associated
// run state and the entry time are stamped by the service when the entry is
// accepted; callers supply only the sequence, author, and message.
type HandoverEntry struct {
	Sequence  int       `json:"sequence"`
	Author    string    `json:"author"`
	Message   string    `json:"message"`
	RunState  RunState  `json:"run_state"`
	CreatedAt time.Time `json:"created_at"`
}

func ValidateHandoverAuthor(value string) error {
	author := strings.TrimSpace(value)
	if author == "" {
		return FieldError(CodeInvalidInput, "author", "must not be empty")
	}
	if len([]rune(author)) > 100 {
		return FieldError(CodeInvalidInput, "author",
			"must not exceed 100 characters")
	}
	return nil
}

func ValidateHandoverMessage(value string) error {
	message := strings.TrimSpace(value)
	if message == "" {
		return FieldError(CodeInvalidInput, "message", "must not be empty")
	}
	if len([]rune(message)) > 2000 {
		return FieldError(CodeInvalidInput, "message",
			"must not exceed 2000 characters")
	}
	return nil
}
