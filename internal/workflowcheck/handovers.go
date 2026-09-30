package workflowcheck

import (
	"fmt"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

// handoverLogWorkflow exercises chronological shift-handover appends:
// version-gated appends, repeated submissions, stale versions, read-only logs
// after terminal transitions, and consistent ordering across detail, list,
// and dedicated log queries.
func handoverLogWorkflow(api *client) error {
	formula, err := ensureApprovedFormula(api, "Handover Formula")
	if err != nil {
		return err
	}
	vessel, err := createCheckVessel(api, "VSL-303", 300)
	if err != nil {
		return err
	}
	started, err := createStartedRun(api, formula.ID, vessel.ID, "RUN-HANDOVER-A")
	if err != nil {
		return err
	}
	runID := started.Run.ID
	version := started.Run.Version

	firstResponse, err := appendHandover(api, runID, map[string]any{
		"author":       "  Mara Chen  ",
		"message":      "Active fermentation started; gravity looks nominal.",
		"client_token": "token-shift-001",
	}, &version)
	if err != nil {
		return err
	}
	if err := requireStatus(firstResponse, 200, "append first handover"); err != nil {
		return err
	}
	var firstAppend handoverAppendView
	if err := firstResponse.decode(&firstAppend); err != nil {
		return err
	}
	if firstAppend.Entry.Sequence != 1 ||
		firstAppend.Entry.Author != "Mara Chen" ||
		firstAppend.Entry.State != domain.RunFermenting ||
		firstAppend.Run.Version <= version {
		return errInvalidWorkflow("first handover entry was not stored correctly")
	}
	version = firstAppend.Run.Version

	secondResponse, err := appendHandover(api, runID, map[string]any{
		"author":       "Ivo Park",
		"message":      "Handed over overnight; temperature held at 20C.",
		"client_token": "token-shift-002",
	}, &version)
	if err != nil {
		return err
	}
	if err := requireStatus(secondResponse, 200, "append second handover"); err != nil {
		return err
	}
	var secondAppend handoverAppendView
	if err := secondResponse.decode(&secondAppend); err != nil {
		return err
	}
	if secondAppend.Entry.Sequence != 2 ||
		secondAppend.Entry.State != domain.RunFermenting {
		return errInvalidWorkflow("second handover entry was not stored correctly")
	}
	version = secondAppend.Run.Version

	// A repeated submission carrying the same client token must be rejected
	// without changing the run or its version.
	duplicateResponse, err := appendHandover(api, runID, map[string]any{
		"author":       "Ivo Park",
		"message":      "Retried overnight note after a connection drop.",
		"client_token": "token-shift-002",
	}, &version)
	if err != nil {
		return err
	}
	if err := requireCode(duplicateResponse, 409, string(domain.CodeHandoverConflict),
		"repeat handover submission"); err != nil {
		return err
	}

	// A stale expected version must be rejected before any state changes.
	staleResponse, err := appendHandover(api, runID, map[string]any{
		"author":       "Ivo Park",
		"message":      "Attempted append based on an outdated view.",
		"client_token": "token-shift-stale",
	}, ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireCode(staleResponse, 412, string(domain.CodeRevisionConflict),
		"append handover with stale version"); err != nil {
		return err
	}

	// A missing expected version is a malformed write, not an optimistic
	// concurrency bypass.
	missingVersionResponse, err := api.call("POST", "/v1/runs/"+runID+"/handovers",
		map[string]any{
			"author":       "Ivo Park",
			"message":      "Attempted append without a version header.",
			"client_token": "token-shift-no-version",
		}, nil)
	if err != nil {
		return err
	}
	if err := requireCode(missingVersionResponse, 400, string(domain.CodeInvalidInput),
		"append handover without expected version"); err != nil {
		return err
	}

	blankAuthorResponse, err := appendHandover(api, runID, map[string]any{
		"author":       "   ",
		"message":      "Entry without an author.",
		"client_token": "token-shift-blank",
	}, &version)
	if err != nil {
		return err
	}
	if err := requireCode(blankAuthorResponse, 400, string(domain.CodeInvalidInput),
		"append handover without author"); err != nil {
		return err
	}

	// The failed writes must not have altered the run or its log.
	if err := requireHandoverOrder(api, runID, 2, []domain.RunState{
		domain.RunFermenting, domain.RunFermenting,
	}); err != nil {
		return err
	}

	// Append observations and move the run toward completion. Entries written
	// at different states must retain the state associated at write time.
	base := time.Now().UTC().Add(10 * time.Second)
	points := []struct {
		gravity     float64
		temperature float64
	}{
		{gravity: 1.050, temperature: 20.0},
		{gravity: 1.028, temperature: 20.5},
		{gravity: 1.014, temperature: 19.8},
	}
	for index, point := range points {
		response, err := api.call("POST", "/v1/runs/"+runID+"/observations",
			map[string]any{
				"sequence":      index + 1,
				"gravity":       point.gravity,
				"temperature_c": point.temperature,
				"observed_at":   verifiedAt(base, index+1),
				"note":          fmt.Sprintf("observation %d", index+1),
			}, &version)
		if err != nil {
			return err
		}
		if err := requireStatus(response, 200, "append observation"); err != nil {
			return err
		}
		var updated runView
		if err := response.decode(&updated); err != nil {
			return err
		}
		version = updated.Version
	}

	conditioningResponse, err := api.call("POST",
		"/v1/runs/"+runID+"/conditioning", nil, &version)
	if err != nil {
		return err
	}
	if err := requireStatus(conditioningResponse, 200, "begin conditioning"); err != nil {
		return err
	}
	var conditioning runView
	if err := conditioningResponse.decode(&conditioning); err != nil {
		return err
	}
	version = conditioning.Version

	conditioningNoteResponse, err := appendHandover(api, runID, map[string]any{
		"author":       "Mara Chen",
		"message":      "Conditioning started; ready to package tomorrow.",
		"client_token": "token-shift-conditioning",
	}, &version)
	if err != nil {
		return err
	}
	if err := requireStatus(conditioningNoteResponse, 200,
		"append conditioning handover"); err != nil {
		return err
	}
	var conditioningAppend handoverAppendView
	if err := conditioningNoteResponse.decode(&conditioningAppend); err != nil {
		return err
	}
	if conditioningAppend.Entry.State != domain.RunConditioning {
		return errInvalidWorkflow("handover entry did not capture conditioning state")
	}
	version = conditioningAppend.Run.Version

	completeResponse, err := api.call("POST", "/v1/runs/"+runID+"/complete",
		nil, &version)
	if err != nil {
		return err
	}
	if err := requireStatus(completeResponse, 200, "complete run"); err != nil {
		return err
	}
	var completed pairView
	if err := completeResponse.decode(&completed); err != nil {
		return err
	}
	version = completed.Run.Version

	// Completed runs are read-only: the log rejects appends but stays queryable.
	afterCompleteResponse, err := appendHandover(api, runID, map[string]any{
		"author":       "Mara Chen",
		"message":      "Attempted post-completion note.",
		"client_token": "token-shift-after-complete",
	}, &version)
	if err != nil {
		return err
	}
	if err := requireCode(afterCompleteResponse, 409, string(domain.CodeRunState),
		"append handover after completion"); err != nil {
		return err
	}

	// Completing twice is a terminal-state write and must not alter run facts.
	repeatCompleteResponse, err := api.call("POST", "/v1/runs/"+runID+"/complete",
		nil, &version)
	if err != nil {
		return err
	}
	if err := requireCode(repeatCompleteResponse, 409, string(domain.CodeRunState),
		"complete run twice"); err != nil {
		return err
	}

	expectedStates := []domain.RunState{
		domain.RunFermenting,
		domain.RunFermenting,
		domain.RunConditioning,
	}
	if err := requireHandoverOrder(api, runID, 3, expectedStates); err != nil {
		return err
	}

	detailResponse, err := api.call("GET", "/v1/runs/"+runID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(detailResponse, 200, "read completed run detail"); err != nil {
		return err
	}
	var detail runView
	if err := detailResponse.decode(&detail); err != nil {
		return err
	}
	if detail.State != domain.RunCompleted || detail.Version != version {
		return errInvalidWorkflow("completed run facts changed after rejected writes")
	}
	if len(detail.HandoverLog) != 3 {
		return errInvalidWorkflow("run detail did not present the complete handover log")
	}

	listResponse, err := api.call("GET", "/v1/runs", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(listResponse, 200, "list runs"); err != nil {
		return err
	}
	var list runListView
	if err := listResponse.decode(&list); err != nil {
		return err
	}
	var listed *runView
	for index := range list.Runs {
		if list.Runs[index].ID == runID {
			listed = &list.Runs[index]
			break
		}
	}
	if listed == nil {
		return errInvalidWorkflow("completed run missing from run list")
	}
	if len(listed.HandoverLog) != 3 ||
		listed.HandoverLog[0].ID != detail.HandoverLog[0].ID ||
		listed.HandoverLog[2].State != domain.RunConditioning {
		return errInvalidWorkflow("run list log did not match run detail log")
	}

	// A second run reaches the aborted terminal state, where the log is also
	// read-only but remains readable.
	abortedRun, err := createPlannedRun(api, formula.ID, "RUN-HANDOVER-B")
	if err != nil {
		return err
	}
	abortVersion := abortedRun.Version
	plannedNoteResponse, err := appendHandover(api, abortedRun.ID, map[string]any{
		"author":       "Ivo Park",
		"message":      "Run created but not yet scheduled on a vessel.",
		"client_token": "token-abort-planned",
	}, &abortVersion)
	if err != nil {
		return err
	}
	if err := requireStatus(plannedNoteResponse, 200, "append planned handover"); err != nil {
		return err
	}
	var plannedAppend handoverAppendView
	if err := plannedNoteResponse.decode(&plannedAppend); err != nil {
		return err
	}
	if plannedAppend.Entry.State != domain.RunPlanned {
		return errInvalidWorkflow("planned handover entry captured the wrong state")
	}
	abortVersion = plannedAppend.Run.Version

	abortResponse, err := api.call("POST", "/v1/runs/"+abortedRun.ID+"/abort",
		nil, &abortVersion)
	if err != nil {
		return err
	}
	if err := requireStatus(abortResponse, 200, "abort run"); err != nil {
		return err
	}
	var abortedPair pairView
	if err := abortResponse.decode(&abortedPair); err != nil {
		return err
	}
	abortVersion = abortedPair.Run.Version

	afterAbortResponse, err := appendHandover(api, abortedRun.ID, map[string]any{
		"author":       "Ivo Park",
		"message":      "Attempted note after abort.",
		"client_token": "token-after-abort",
	}, &abortVersion)
	if err != nil {
		return err
	}
	if err := requireCode(afterAbortResponse, 409, string(domain.CodeRunState),
		"append handover after abort"); err != nil {
		return err
	}

	abortLogResponse, err := api.call("GET",
		"/v1/runs/"+abortedRun.ID+"/handovers", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(abortLogResponse, 200, "read aborted run handover log"); err != nil {
		return err
	}
	var abortLog handoverLogView
	if err := abortLogResponse.decode(&abortLog); err != nil {
		return err
	}
	if len(abortLog.HandoverLog) != 1 ||
		abortLog.HandoverLog[0].State != domain.RunPlanned {
		return errInvalidWorkflow("aborted run handover log was not preserved")
	}

	missingResponse, err := api.call("GET", "/v1/runs/run_missing/handovers", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(missingResponse, 404, string(domain.CodeRunNotFound),
		"read handover log for missing run"); err != nil {
		return err
	}
	return nil
}

func appendHandover(api *client, runID string, payload map[string]any, expected *int64) (response, error) {
	return api.call("POST", "/v1/runs/"+runID+"/handovers", payload, expected)
}

func createPlannedRun(api *client, formulaID, code string) (runView, error) {
	response, err := api.call("POST", "/v1/runs",
		createRunPayload(code, formulaID), nil)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 201, "create planned run"); err != nil {
		return runView{}, err
	}
	var run runView
	if err := response.decode(&run); err != nil {
		return runView{}, err
	}
	return run, nil
}

func requireHandoverOrder(
	api *client,
	runID string,
	count int,
	states []domain.RunState,
) error {
	response, err := api.call("GET", "/v1/runs/"+runID+"/handovers", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(response, 200, "read handover log"); err != nil {
		return err
	}
	var view handoverLogView
	if err := response.decode(&view); err != nil {
		return err
	}
	if len(view.HandoverLog) != count {
		return errInvalidWorkflow(fmt.Sprintf(
			"handover log length = %d, want %d", len(view.HandoverLog), count))
	}
	var previous time.Time
	for index, entry := range view.HandoverLog {
		if entry.Sequence != index+1 {
			return errInvalidWorkflow("handover log is not in sequence order")
		}
		if index < len(states) && entry.State != states[index] {
			return errInvalidWorkflow(fmt.Sprintf(
				"handover entry %d state = %q, want %q",
				index+1, entry.State, states[index]))
		}
		if index > 0 && entry.CreatedAt.Before(previous) {
			return errInvalidWorkflow("handover log timestamps are not chronological")
		}
		previous = entry.CreatedAt
	}
	return nil
}
