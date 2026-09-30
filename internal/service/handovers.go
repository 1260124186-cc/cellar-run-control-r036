package service

import "github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"

// AppendHandover adds one shift-handover entry to a run. The append is
// accepted only when the run exists and the supplied expected version matches
// the current run version. A repeated client token, a stale version, or a
// write against a completed or aborted run fails before any state is
// persisted.
func (s *Service) AppendHandover(
	id string,
	input HandoverInput,
) (domain.FermentationRun, domain.HandoverEntry, error) {
	var updated domain.FermentationRun
	var appended domain.HandoverEntry
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		entry := domain.HandoverEntry{
			Author:      input.Author,
			Message:     input.Message,
			ClientToken: input.ClientToken,
		}
		result, err := run.AppendHandover(entry, s.clock.Now())
		if err != nil {
			return err
		}
		snapshot.Runs[id] = run.Clone()
		updated = run.Clone()
		appended = result
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.HandoverEntry{}, err
	}
	return updated, appended, nil
}

// ListHandovers returns the handover log of one run in its deterministic
// chronological order. The log remains readable after a run completes or is
// aborted.
func (s *Service) ListHandovers(id string) ([]domain.HandoverEntry, error) {
	var entries []domain.HandoverEntry
	err := s.store.View(func(snapshot domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		entries = append([]domain.HandoverEntry{}, run.HandoverLog...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}
