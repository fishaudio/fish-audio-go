package fishaudio

import (
	"context"
	"net/http"
)

// Credits represents the user's API credit balance.
type Credits struct {
	ID             string `json:"_id"`
	UserID         string `json:"user_id"`
	Credit         string `json:"credit"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	HasPhoneSHA256 *bool  `json:"has_phone_sha256,omitempty"`
	HasFreeCredit  *bool  `json:"has_free_credit,omitempty"`
}

// Package represents the user's prepaid package information.
type Package struct {
	ID         string  `json:"_id"`
	UserID     string  `json:"user_id"`
	Type       string  `json:"type"`
	Total      int     `json:"total"`
	Balance    int     `json:"balance"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	FinishedAt *string `json:"finished_at,omitempty"`
}

// GetCreditsParams contains parameters for getting credits.
type GetCreditsParams struct {
	// CheckFreeCredit indicates whether to check free credit availability.
	CheckFreeCredit bool
}

// AccountService provides account and billing operations.
type AccountService struct {
	client *Client
}

// GetCredits returns the API credit balance.
//
// Example:
//
//	credits, err := client.Account.GetCredits(ctx, nil)
//	fmt.Printf("Available credits: %s\n", credits.Credit)
func (s *AccountService) GetCredits(ctx context.Context, params *GetCreditsParams) (*Credits, error) {
	path := "/wallet/self/api-credit"
	if params != nil && params.CheckFreeCredit {
		path += "?check_free_credit=true"
	}

	var result Credits
	if err := s.client.doJSONRequest(ctx, http.MethodGet, path, nil, &result, nil); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetPackage returns the user's package information.
//
// Example:
//
//	pkg, err := client.Account.GetPackage(ctx)
//	fmt.Printf("Balance: %d/%d\n", pkg.Balance, pkg.Total)
func (s *AccountService) GetPackage(ctx context.Context) (*Package, error) {
	var result Package
	if err := s.client.doJSONRequest(ctx, http.MethodGet, "/wallet/self/package", nil, &result, nil); err != nil {
		return nil, err
	}

	return &result, nil
}
