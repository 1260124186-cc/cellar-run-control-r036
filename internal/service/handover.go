package service

import "github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"

func (s *Service) AppendHandover(
	id string,
	input HandoverInput,
) (domain.FermentationRun, error) {
	var updated domain.FermentationRun
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if err := run.AppendHandover(
			input.Sequence, input.Author, input.Message, s.clock.Now(),
		); err != nil {
			return err
		}
		snapshot.Runs[id] = run.Clone()
		updated = run.Clone()
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, err
	}
	return updated, nil
}
