package store

import "encoding/json"

// RecordAudit inserts an audit event.
func (s *Store) RecordAudit(actor, action, subjectType, subjectID string, detail any) error {
	var detailJSON string
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		detailJSON = string(b)
	}
	_, err := s.db.Exec(`
		INSERT INTO audit_events (at, actor, action, subject_type, subject_id, detail)
		VALUES (?,?,?,?,?,?)`, Now(), actor, action, nullable(subjectType), nullable(subjectID), nullable(detailJSON))
	return err
}
