package handlers

import "testing"

func TestAttachedAccountsCountsIRCClientsOnly(t *testing.T) {
	// user 1: an IRC client and a web chat; user 2: only the web app open
	accounts, web := attachedAccounts(map[int64]int{1: 2, 2: 1}, map[int64]int{1: 1})
	if accounts != 1 || web != 2 {
		t.Fatalf("accounts %d web %d — want 1 and 2", accounts, web)
	}
}
