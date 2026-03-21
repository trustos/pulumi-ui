package services

import (
	"fmt"

	"github.com/trustos/pulumi-ui/internal/db"
)

// PassphraseService enforces business rules for passphrase lifecycle operations.
// The referential-integrity guard (cannot delete a passphrase that stacks still
// reference) lives here — not in the store — because it crosses table boundaries.
type PassphraseService struct {
	Passphrases *db.PassphraseStore
	Stacks      *db.StackStore
}

func NewPassphraseService(passphrases *db.PassphraseStore, stacks *db.StackStore) *PassphraseService {
	return &PassphraseService{Passphrases: passphrases, Stacks: stacks}
}

// Delete refuses deletion if any stacks still reference the passphrase.
func (s *PassphraseService) Delete(id string) error {
	count, err := s.Stacks.CountByPassphrase(id)
	if err != nil {
		return fmt.Errorf("check stack references: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("passphrase is used by %d stack(s) — remove those stacks first", count)
	}
	return s.Passphrases.Delete(id)
}
