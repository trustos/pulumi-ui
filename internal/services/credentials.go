package services

import (
	"fmt"

	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/engine"
)

// CredentialService resolves the full engine.Credentials for a stack operation.
// It is the authoritative place for the credential-fallback business rule:
//
//  1. If ociAccountID is set → load that account's credentials.
//  2. Else → fall back to global credentials from the credential store.
//  3. If sshKeyID is set → override the account's SSH public key.
//  4. passphraseID is always required.
//
// db.OCICredentials does not appear in any caller outside this package and
// internal/db — engine.Credentials is the explicit boundary.
type CredentialService struct {
	Accounts    *db.AccountStore
	Passphrases *db.PassphraseStore
	SSHKeys     *db.SSHKeyStore
	Creds       *db.CredentialStore
}

func NewCredentialService(
	accounts *db.AccountStore,
	passphrases *db.PassphraseStore,
	sshKeys *db.SSHKeyStore,
	creds *db.CredentialStore,
) *CredentialService {
	return &CredentialService{
		Accounts:    accounts,
		Passphrases: passphrases,
		SSHKeys:     sshKeys,
		Creds:       creds,
	}
}

// Resolve returns engine.Credentials ready for use by the Pulumi engine.
func (s *CredentialService) Resolve(ociAccountID, passphraseID, sshKeyID *string) (engine.Credentials, error) {
	var oci db.OCICredentials
	var err error

	if ociAccountID != nil && *ociAccountID != "" {
		account, err := s.Accounts.Get(*ociAccountID)
		if err != nil {
			return engine.Credentials{}, fmt.Errorf("load OCI account: %w", err)
		}
		if account == nil {
			return engine.Credentials{}, fmt.Errorf("OCI account not found")
		}
		oci = account.ToOCICredentials()
	} else {
		oci, err = s.Creds.GetOCICredentials()
		if err != nil {
			return engine.Credentials{}, fmt.Errorf("load global OCI credentials: %w", err)
		}
	}

	// If a dedicated SSH key is linked, override the account's SSH public key.
	if sshKeyID != nil && *sshKeyID != "" {
		sshPub, err := s.SSHKeys.GetPublicKey(*sshKeyID)
		if err != nil {
			return engine.Credentials{}, fmt.Errorf("load SSH key: %w", err)
		}
		oci.SSHPublicKey = sshPub
	}

	if passphraseID == nil || *passphraseID == "" {
		return engine.Credentials{}, fmt.Errorf("no passphrase assigned to this stack — assign one in Settings")
	}
	passphrase, err := s.Passphrases.GetValue(*passphraseID)
	if err != nil {
		return engine.Credentials{}, fmt.Errorf("load passphrase: %w", err)
	}

	return engine.Credentials{OCI: oci, Passphrase: passphrase}, nil
}
