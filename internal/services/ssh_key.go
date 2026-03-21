package services

import (
	"fmt"

	"github.com/trustos/pulumi-ui/internal/db"
)

// SSHKeyService enforces business rules for SSH key lifecycle operations.
// The referential-integrity guard (cannot delete a key that stacks still
// reference) lives here — not in the store — because it crosses table boundaries.
type SSHKeyService struct {
	SSHKeys *db.SSHKeyStore
	Stacks  *db.StackStore
}

func NewSSHKeyService(sshKeys *db.SSHKeyStore, stacks *db.StackStore) *SSHKeyService {
	return &SSHKeyService{SSHKeys: sshKeys, Stacks: stacks}
}

// Delete refuses deletion if any stacks still reference the SSH key.
func (s *SSHKeyService) Delete(id string) error {
	count, err := s.Stacks.CountBySSHKey(id)
	if err != nil {
		return fmt.Errorf("check stack references: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("SSH key is used by %d stack(s) — unlink it from those stacks first", count)
	}
	return s.SSHKeys.Delete(id)
}
