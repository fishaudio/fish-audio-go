package fishaudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccountService_GetCredits_DefaultParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodGet)
		}
		if !strings.HasPrefix(r.URL.Path, "/wallet/self/api-credit") {
			t.Errorf("Path = %q, want prefix %q", r.URL.Path, "/wallet/self/api-credit")
		}

		// Should not have check_free_credit param
		if r.URL.Query().Get("check_free_credit") != "" {
			t.Error("check_free_credit should not be set for nil params")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Credits{
			ID:     "credit-123",
			UserID: "user-456",
			Credit: "100.50",
		})
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	credits, err := client.Account.GetCredits(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetCredits() error = %v", err)
	}

	if credits.ID != "credit-123" {
		t.Errorf("ID = %q, want %q", credits.ID, "credit-123")
	}
	if credits.Credit != "100.50" {
		t.Errorf("Credit = %q, want %q", credits.Credit, "100.50")
	}
}

func TestAccountService_GetCredits_WithCheckFreeCredit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("check_free_credit") != "true" {
			t.Errorf("check_free_credit = %q, want %q", r.URL.Query().Get("check_free_credit"), "true")
		}

		hasFreeCredit := true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Credits{
			ID:            "credit-123",
			Credit:        "50.00",
			HasFreeCredit: &hasFreeCredit,
		})
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	credits, err := client.Account.GetCredits(context.Background(), &GetCreditsParams{
		CheckFreeCredit: true,
	})
	if err != nil {
		t.Fatalf("GetCredits() error = %v", err)
	}

	if credits.HasFreeCredit == nil || !*credits.HasFreeCredit {
		t.Error("HasFreeCredit should be true")
	}
}

func TestAccountService_GetPackage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/wallet/self/package" {
			t.Errorf("Path = %q, want %q", r.URL.Path, "/wallet/self/package")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Package{
			ID:      "pkg-123",
			UserID:  "user-456",
			Type:    "premium",
			Total:   1000,
			Balance: 750,
		})
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	pkg, err := client.Account.GetPackage(context.Background())
	if err != nil {
		t.Fatalf("GetPackage() error = %v", err)
	}

	if pkg.ID != "pkg-123" {
		t.Errorf("ID = %q, want %q", pkg.ID, "pkg-123")
	}
	if pkg.Type != "premium" {
		t.Errorf("Type = %q, want %q", pkg.Type, "premium")
	}
	if pkg.Total != 1000 {
		t.Errorf("Total = %d, want %d", pkg.Total, 1000)
	}
	if pkg.Balance != 750 {
		t.Errorf("Balance = %d, want %d", pkg.Balance, 750)
	}
}

func TestAccountService_GetCredits_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "unauthorized"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	_, err := client.Account.GetCredits(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var authErr *AuthenticationError
	if !containsError(err, &authErr) {
		t.Errorf("expected AuthenticationError, got %T", err)
	}
}

func TestAccountService_GetPackage_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "not found"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	_, err := client.Account.GetPackage(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var notFoundErr *NotFoundError
	if !containsError(err, &notFoundErr) {
		t.Errorf("expected NotFoundError, got %T", err)
	}
}

func TestCredits_Fields(t *testing.T) {
	hasFreeCredit := true
	hasPhone := false
	credits := Credits{
		ID:             "id",
		UserID:         "user",
		Credit:         "100.00",
		CreatedAt:      "2024-01-01",
		UpdatedAt:      "2024-01-02",
		HasFreeCredit:  &hasFreeCredit,
		HasPhoneSHA256: &hasPhone,
	}

	if credits.ID != "id" {
		t.Errorf("ID = %q, want %q", credits.ID, "id")
	}
	if credits.HasFreeCredit == nil || !*credits.HasFreeCredit {
		t.Error("HasFreeCredit should be true")
	}
	if credits.HasPhoneSHA256 == nil || *credits.HasPhoneSHA256 {
		t.Error("HasPhoneSHA256 should be false")
	}
}

func TestPackage_Fields(t *testing.T) {
	finishedAt := "2024-12-31"
	pkg := Package{
		ID:         "id",
		UserID:     "user",
		Type:       "basic",
		Total:      500,
		Balance:    250,
		CreatedAt:  "2024-01-01",
		UpdatedAt:  "2024-01-02",
		FinishedAt: &finishedAt,
	}

	if pkg.ID != "id" {
		t.Errorf("ID = %q, want %q", pkg.ID, "id")
	}
	if pkg.FinishedAt == nil || *pkg.FinishedAt != "2024-12-31" {
		t.Errorf("FinishedAt = %v, want %q", pkg.FinishedAt, "2024-12-31")
	}
}
