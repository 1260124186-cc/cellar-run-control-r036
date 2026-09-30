package domain

import (
	"strings"
	"time"
)

// MaxHandoverEntries bounds the handover log carried by a single run.
const MaxHandoverEntries = 1000

const (
	maxHandoverAuthorRunes  = 100
	maxHandoverMessageRunes = 2000
)

// HandoverEntry is one chronological shift-handover note attached to a run.
// Sequence is contiguous and begins at one; State records the run state that
// was current when the on-duty operator wrote the entry.
type HandoverEntry struct {
	ID          string    `json:"id"`
	Sequence    int       `json:"sequence"`
	Author      string    `json:"author"`
	Message     string    `json:"message"`
	State       RunState  `json:"state"`
	ClientToken string    `json:"client_token,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Valid reports whether the run state is one of the declared lifecycle states.
func (s RunState) Valid() bool {
	switch s {
	case RunPlanned, RunReserved, RunFermenting,
		RunConditioning, RunCompleted, RunAborted:
		return true
	default:
		return false
	}
}

// Terminal reports whether no further mutation (including handover appends) is
// allowed.
func (s RunState) Terminal() bool {
	return s == RunCompleted || s == RunAborted
}

// Validate checks an entry supplied by a caller before it is attached to a run.
func (e HandoverEntry) Validate() error {
	if e.Author == "" {
		return FieldError(CodeInvalidInput, "author", "must not be empty")
	}
	if len([]rune(e.Author)) > maxHandoverAuthorRunes {
		return FieldError(CodeInvalidInput, "author",
			"must not exceed 100 characters")
	}
	if e.Message == "" {
		return FieldError(CodeInvalidInput, "message", "must not be empty")
	}
	if len([]rune(e.Message)) > maxHandoverMessageRunes {
		return FieldError(CodeInvalidInput, "message",
			"must not exceed 2000 characters")
	}
	if e.ClientToken != "" {
		if err := ValidateClientToken(e.ClientToken, "client_token"); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeHandoverText trims the outer whitespace of an operator-supplied
// handover field.
func NormalizeHandoverText(value string) string {
	return strings.TrimSpace(value)
}

// AppendHandover attaches a chronological handover entry to the run. Only a
// run in a non-terminal state accepts entries. A non-empty client token that
// matches an earlier entry rejects a repeated submission. The entry records
// the run state that is current at the time of the append, so later
// transitions never rewrite the associated state of an older entry.
func (r *FermentationRun) AppendHandover(entry HandoverEntry, now time.Time) (HandoverEntry, error) {
	if r.State.Terminal() {
		return HandoverEntry{}, NewError(CodeRunState,
			"completed or aborted runs are read-only")
	}
	if len(r.HandoverLog) >= MaxHandoverEntries {
		return HandoverEntry{}, NewError(CodeHandoverConflict,
			"run cannot contain more than 1000 handover entries")
	}
	entry.Author = NormalizeHandoverText(entry.Author)
	entry.Message = NormalizeHandoverText(entry.Message)
	if entry.ClientToken != "" {
		for _, existing := range r.HandoverLog {
			if existing.ClientToken == entry.ClientToken {
				return HandoverEntry{}, NewError(CodeHandoverConflict,
					"a handover entry with this client token already exists")
			}
		}
	}
	if err := entry.Validate(); err != nil {
		return HandoverEntry{}, err
	}
	stamp := now.UTC()
	identifier, err := NewID("hnd")
	if err != nil {
		return HandoverEntry{}, NewError(CodeStorage, err.Error())
	}
	entry.ID = identifier
	entry.Sequence = len(r.HandoverLog) + 1
	entry.State = r.State
	entry.CreatedAt = stamp
	r.HandoverLog = append(r.HandoverLog, entry)
	r.UpdatedAt = stamp
	r.Version++
	return entry, nil
}

// ValidateHandoverLog verifies the persisted ordering and identity invariants
// of a complete handover log. Entries must be contiguous from sequence one,
// have unique identifiers and client tokens, reference valid run states, and
// carry a creation timestamp.
func ValidateHandoverLog(entries []HandoverEntry) error {
	seenIDs := make(map[string]struct{}, len(entries))
	seenTokens := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		if entry.Sequence != index+1 {
			return NewError(CodeStorage,
				"handover log sequences must be contiguous and begin at 1")
		}
		if entry.ID == "" {
			return NewError(CodeStorage, "handover entry is missing its identifier")
		}
		if _, exists := seenIDs[entry.ID]; exists {
			return NewError(CodeStorage, "handover log contains a duplicate identifier")
		}
		seenIDs[entry.ID] = struct{}{}
		if !entry.State.Valid() {
			return NewError(CodeStorage, "handover entry references an unknown run state")
		}
		if entry.CreatedAt.IsZero() {
			return NewError(CodeStorage, "handover entry is missing its creation time")
		}
		if err := entry.Validate(); err != nil {
			return err
		}
		if entry.ClientToken != "" {
			if _, exists := seenTokens[entry.ClientToken]; exists {
				return NewError(CodeStorage,
					"handover log contains a duplicate client token")
			}
			seenTokens[entry.ClientToken] = struct{}{}
		}
	}
	return nil
}
