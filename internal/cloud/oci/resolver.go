package oci

import (
	"fmt"

	"github.com/trustos/pulumi-ui/internal/cloud"
	"github.com/trustos/pulumi-ui/internal/ports"
)

// AccountAdapter bridges an OCI-specific account repository to the
// cloud.AccountResolver interface used by the Registry.
type AccountAdapter struct {
	Accounts ports.AccountRepository
}

func NewAccountAdapter(accounts ports.AccountRepository) *AccountAdapter {
	return &AccountAdapter{Accounts: accounts}
}

// Resolve returns the ResolvedAccount shape expected by the Registry.
// The API-key fingerprint acts as the credentials-identity value — it
// changes on key rotation, giving the Registry cache a natural eviction
// trigger without a schema migration.
func (a *AccountAdapter) Resolve(accountID string) (cloud.ResolvedAccount, error) {
	row, err := a.Accounts.Get(accountID)
	if err != nil {
		return cloud.ResolvedAccount{}, err
	}
	if row == nil {
		return cloud.ResolvedAccount{}, fmt.Errorf("oci resolver: %w: account %q", cloud.ErrNotFound, accountID)
	}
	return cloud.ResolvedAccount{
		ProviderID:             ProviderID,
		CredentialsFingerprint: row.Fingerprint,
		Credentials: cloud.Credentials{
			Region: row.Region,
			Fields: map[string]string{
				"tenancyOCID": row.TenancyOCID,
				"userOCID":    row.UserOCID,
				"fingerprint": row.Fingerprint,
				"privateKey":  row.PrivateKey,
			},
		},
	}, nil
}
