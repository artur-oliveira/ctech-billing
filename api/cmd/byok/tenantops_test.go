package main

import "testing"

func TestOperatorActionsNeedLiveANamedOperatorAndATenant(t *testing.T) {
	for name, args := range map[string][3]string{
		"default mode": {"both", "artur", "ctech"},
		"test mode":    {"test", "artur", "ctech"},
		"no operator":  {"live", "", "ctech"},
		"no tenant":    {"live", "artur", ""},
	} {
		if operatorArgs(args[0], args[1], args[2]) == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := operatorArgs("live", "artur", "ctech"); err != nil {
		t.Fatal(err)
	}
}

func TestLast4NeverShowsMoreThanTheEnd(t *testing.T) {
	if last4("39053344705") != "4705" || last4("12") != "12" {
		t.Fatal("last4")
	}
}
