// Package views contains the individual view models for the trade-tracker TUI.
package views

import pb "trade-tracker-go/gen/tradetracker/v1"

// AllAccountsID is the sentinel value meaning "no account filter applied".
const AllAccountsID = ""

// SharedState is the subset of app state that every view needs.
// Passed by value into Update so views cannot mutate app state directly.
type SharedState struct {
	Accounts          []*pb.Account
	SelectedAccountID string // AllAccountsID means all accounts
	Width             int
	Height            int
}

// SelectedAccount returns the Account for SelectedAccountID, or nil for All Accounts.
func (s SharedState) SelectedAccount() *pb.Account {
	if s.SelectedAccountID == AllAccountsID {
		return nil
	}
	for _, a := range s.Accounts {
		if a.Id == s.SelectedAccountID {
			return a
		}
	}
	return nil
}

// AccountLabel returns a display string for the given account.
func AccountLabel(a *pb.Account) string {
	if a.Name != "" {
		return a.Name
	}
	return a.Broker + " " + a.AccountNumber
}

// LoadMsg is sent to a view when it should (re-)fetch its data.
// Sent on initial view activation and whenever the account selector changes.
type LoadMsg struct {
	State SharedState
}

// accountIDs returns the list of account IDs to query given the selector.
// If SelectedAccountID is set, returns a single-element slice; otherwise all.
func accountIDs(state SharedState) []string {
	if state.SelectedAccountID != AllAccountsID {
		return []string{state.SelectedAccountID}
	}
	ids := make([]string, len(state.Accounts))
	for i, a := range state.Accounts {
		ids[i] = a.Id
	}
	return ids
}
