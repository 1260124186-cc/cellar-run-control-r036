package workflowcheck

import (
	"fmt"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func runHandoverLog(api *client) error {
	formula, err := ensureApprovedFormula(api, "Handover Formula")
	if err != nil {
		return err
	}
	vessel, err := createCheckVessel(api, "VSL-303", 300)
	if err != nil {
		return err
	}

	runResponse, err := api.call("POST", "/v1/runs",
		createRunPayload("RUN-HANDOVER", formula.ID), nil)
	if err != nil {
		return err
	}
	if err := requireStatus(runResponse, 201, "create handover run"); err != nil {
		return err
	}
	var run runView
	if err := runResponse.decode(&run); err != nil {
		return err
	}

	if err := requireEmptyHandover(api, run.ID); err != nil {
		return err
	}

	first, err := appendHandover(api, run.ID, 1, "Alice", "night shift: run created",
		ptrInt64(1), "append first handover entry")
	if err != nil {
		return err
	}
	if err := requireEntry(first.HandoverLog, 0, 1, "Alice", domain.RunPlanned); err != nil {
		return err
	}

	// Replaying the same append must not duplicate the entry or move the run.
	duplicate, err := api.call("POST", "/v1/runs/"+run.ID+"/handover",
		handoverPayload(1, "Alice", "night shift: run created"), ptrInt64(first.Version))
	if err != nil {
		return err
	}
	if err := requireCode(duplicate, 409, string(domain.CodeHandoverConflict),
		"duplicate handover append"); err != nil {
		return err
	}
	if err := requireHandoverCount(api, run.ID, 1, first.Version); err != nil {
		return err
	}

	reserveResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/reserve",
		map[string]any{"vessel_id": vessel.ID}, ptrInt64(first.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(reserveResponse, 200, "reserve handover vessel"); err != nil {
		return err
	}
	var reserved pairView
	if err := reserveResponse.decode(&reserved); err != nil {
		return err
	}

	second, err := appendHandover(api, run.ID, 2, "Bao",
		"vessel reserved, pitching yeast tonight", nil,
		"append entry without a version header")
	if err != nil {
		return err
	}
	if err := requireEntry(second.HandoverLog, 1, 2, "Bao", domain.RunReserved); err != nil {
		return err
	}

	// A stale expected version rejects the append without touching the log.
	stale, err := api.call("POST", "/v1/runs/"+run.ID+"/handover",
		handoverPayload(3, "Chen", "stale attempt"), ptrInt64(reserved.Run.Version))
	if err != nil {
		return err
	}
	if err := requireCode(stale, 412, string(domain.CodeRevisionConflict),
		"stale handover append"); err != nil {
		return err
	}
	if err := requireHandoverCount(api, run.ID, 2, second.Version); err != nil {
		return err
	}

	startResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/start",
		nil, ptrInt64(second.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(startResponse, 200, "start handover run"); err != nil {
		return err
	}
	var started pairView
	if err := startResponse.decode(&started); err != nil {
		return err
	}

	third, err := appendHandover(api, run.ID, 3, "Chen",
		"fermentation active, gravity trending down", ptrInt64(started.Run.Version),
		"append fermenting entry")
	if err != nil {
		return err
	}
	if err := requireEntry(third.HandoverLog, 2, 3, "Chen", domain.RunFermenting); err != nil {
		return err
	}

	blank, err := api.call("POST", "/v1/runs/"+run.ID+"/handover",
		handoverPayload(4, "   ", "missing author"), ptrInt64(third.Version))
	if err != nil {
		return err
	}
	if err := requireCode(blank, 400, string(domain.CodeInvalidInput),
		"reject blank handover author"); err != nil {
		return err
	}
	if err := requireHandoverCount(api, run.ID, 3, third.Version); err != nil {
		return err
	}

	base := time.Now().UTC().Add(10 * time.Second)
	points := []float64{1.050, 1.028, 1.014}
	version := third.Version
	for index, gravity := range points {
		sequence := index + 1
		response, callErr := api.call("POST", "/v1/runs/"+run.ID+"/observations",
			map[string]any{
				"sequence":      sequence,
				"gravity":       gravity,
				"temperature_c": 20.0,
				"observed_at":   verifiedAt(base, sequence),
				"note":          fmt.Sprintf("observation %d", sequence),
			}, ptrInt64(version))
		if callErr != nil {
			return callErr
		}
		if err := requireStatus(response, 200, "append handover check observation"); err != nil {
			return err
		}
		var observed runView
		if err := response.decode(&observed); err != nil {
			return err
		}
		version = observed.Version
	}

	conditioningResponse, err := api.call("POST",
		"/v1/runs/"+run.ID+"/conditioning", nil, ptrInt64(version))
	if err != nil {
		return err
	}
	if err := requireStatus(conditioningResponse, 200, "begin handover conditioning"); err != nil {
		return err
	}
	var conditioning runView
	if err := conditioningResponse.decode(&conditioning); err != nil {
		return err
	}

	fourth, err := appendHandover(api, run.ID, 4, "Dana",
		"moving to conditioning", ptrInt64(conditioning.Version),
		"append conditioning entry")
	if err != nil {
		return err
	}
	if err := requireEntry(fourth.HandoverLog, 3, 4, "Dana", domain.RunConditioning); err != nil {
		return err
	}

	completeResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/complete",
		nil, ptrInt64(fourth.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(completeResponse, 200, "complete handover run"); err != nil {
		return err
	}
	var completed pairView
	if err := completeResponse.decode(&completed); err != nil {
		return err
	}
	if err := requireRunState(completed.Run, domain.RunCompleted); err != nil {
		return err
	}

	// Terminal runs keep the log readable but reject new entries.
	afterComplete, err := api.call("POST", "/v1/runs/"+run.ID+"/handover",
		handoverPayload(5, "Dana", "too late"), ptrInt64(completed.Run.Version))
	if err != nil {
		return err
	}
	if err := requireCode(afterComplete, 409, string(domain.CodeRunState),
		"append handover after completion"); err != nil {
		return err
	}
	if err := requireHandoverCount(api, run.ID, 4, completed.Run.Version); err != nil {
		return err
	}

	detail, err := readRun(api, run.ID, "read completed handover run")
	if err != nil {
		return err
	}
	expectedStates := []domain.RunState{
		domain.RunPlanned, domain.RunReserved,
		domain.RunFermenting, domain.RunConditioning,
	}
	if err := requireLogShape(detail.HandoverLog, expectedStates); err != nil {
		return err
	}
	if err := requireListedHandover(api, run.ID, detail.HandoverLog); err != nil {
		return err
	}

	if err := verifyAbortedRunReadOnly(api, formula.ID); err != nil {
		return err
	}

	missing, err := api.call("POST", "/v1/runs/run_missing/handover",
		handoverPayload(1, "Alice", "no such run"), nil)
	if err != nil {
		return err
	}
	if err := requireCode(missing, 404, string(domain.CodeRunNotFound),
		"append handover to missing run"); err != nil {
		return err
	}
	return nil
}

func handoverPayload(sequence int, author, message string) map[string]any {
	return map[string]any{
		"sequence": sequence,
		"author":   author,
		"message":  message,
	}
}

func appendHandover(
	api *client,
	runID string,
	sequence int,
	author, message string,
	expectedVersion *int64,
	operation string,
) (runView, error) {
	response, err := api.call("POST", "/v1/runs/"+runID+"/handover",
		handoverPayload(sequence, author, message), expectedVersion)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 200, operation); err != nil {
		return runView{}, err
	}
	var run runView
	if err := response.decode(&run); err != nil {
		return runView{}, err
	}
	return run, nil
}

func readRun(api *client, runID, operation string) (runView, error) {
	response, err := api.call("GET", "/v1/runs/"+runID, nil, nil)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 200, operation); err != nil {
		return runView{}, err
	}
	var run runView
	if err := response.decode(&run); err != nil {
		return runView{}, err
	}
	return run, nil
}

func requireEmptyHandover(api *client, runID string) error {
	run, err := readRun(api, runID, "read run with empty handover log")
	if err != nil {
		return err
	}
	if len(run.HandoverLog) != 0 {
		return errInvalidWorkflow("new run should expose an empty handover log")
	}
	return nil
}

func requireHandoverCount(api *client, runID string, entries int, version int64) error {
	run, err := readRun(api, runID, "read run handover count")
	if err != nil {
		return err
	}
	if len(run.HandoverLog) != entries {
		return errInvalidWorkflow(fmt.Sprintf(
			"handover log should hold %d entries, holds %d", entries, len(run.HandoverLog)))
	}
	if run.Version != version {
		return errInvalidWorkflow(fmt.Sprintf(
			"rejected handover append moved run version to %d, want %d",
			run.Version, version))
	}
	return nil
}

func requireEntry(
	log []domain.HandoverEntry,
	index int,
	sequence int,
	author string,
	state domain.RunState,
) error {
	if len(log) <= index {
		return errInvalidWorkflow(fmt.Sprintf(
			"handover log missing entry at index %d", index))
	}
	entry := log[index]
	if entry.Sequence != sequence || entry.Author != author || entry.RunState != state {
		return errInvalidWorkflow(fmt.Sprintf(
			"handover entry %d mismatch: %+v", sequence, entry))
	}
	if entry.Message == "" || entry.CreatedAt.IsZero() {
		return errInvalidWorkflow(fmt.Sprintf(
			"handover entry %d is missing message or timestamp", sequence))
	}
	return nil
}

func requireLogShape(log []domain.HandoverEntry, states []domain.RunState) error {
	if len(log) != len(states) {
		return errInvalidWorkflow(fmt.Sprintf(
			"handover log length is %d, want %d", len(log), len(states)))
	}
	var previous time.Time
	for index, state := range states {
		entry := log[index]
		if entry.Sequence != index+1 {
			return errInvalidWorkflow("handover log is not ordered by contiguous sequence")
		}
		if entry.RunState != state {
			return errInvalidWorkflow(fmt.Sprintf(
				"handover entry %d is associated with %q, want %q",
				entry.Sequence, entry.RunState, state))
		}
		if index > 0 && entry.CreatedAt.Before(previous) {
			return errInvalidWorkflow("handover timestamps are not chronological")
		}
		previous = entry.CreatedAt
	}
	return nil
}

func requireListedHandover(
	api *client,
	runID string,
	detail []domain.HandoverEntry,
) error {
	response, err := api.call("GET", "/v1/runs", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(response, 200, "list runs"); err != nil {
		return err
	}
	var body struct {
		Runs []runView `json:"runs"`
	}
	if err := response.decode(&body); err != nil {
		return err
	}
	for _, listed := range body.Runs {
		if listed.ID != runID {
			continue
		}
		if len(listed.HandoverLog) != len(detail) {
			return errInvalidWorkflow("list and detail disagree on handover length")
		}
		for index, entry := range listed.HandoverLog {
			if entry != detail[index] {
				return errInvalidWorkflow(fmt.Sprintf(
					"list and detail disagree on handover entry %d", index+1))
			}
		}
		return nil
	}
	return errInvalidWorkflow("completed run missing from run list")
}

func verifyAbortedRunReadOnly(api *client, formulaID string) error {
	runResponse, err := api.call("POST", "/v1/runs",
		createRunPayload("RUN-HANDOVER-ABORT", formulaID), nil)
	if err != nil {
		return err
	}
	if err := requireStatus(runResponse, 201, "create abortable handover run"); err != nil {
		return err
	}
	var run runView
	if err := runResponse.decode(&run); err != nil {
		return err
	}
	planned, err := appendHandover(api, run.ID, 1, "Alice",
		"pre-abort note", ptrInt64(run.Version), "append pre-abort entry")
	if err != nil {
		return err
	}
	abortResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/abort",
		nil, ptrInt64(planned.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(abortResponse, 200, "abort handover run"); err != nil {
		return err
	}
	var aborted pairView
	if err := abortResponse.decode(&aborted); err != nil {
		return err
	}
	afterAbort, err := api.call("POST", "/v1/runs/"+run.ID+"/handover",
		handoverPayload(2, "Bao", "post-abort note"), ptrInt64(aborted.Run.Version))
	if err != nil {
		return err
	}
	if err := requireCode(afterAbort, 409, string(domain.CodeRunState),
		"append handover after abort"); err != nil {
		return err
	}
	return requireHandoverCount(api, run.ID, 1, aborted.Run.Version)
}
