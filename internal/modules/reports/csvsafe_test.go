package reports

import "testing"

func TestCSVSafeNeutralisesFormulas(t *testing.T) {
	cases := map[string]string{
		"=HYPERLINK(\"http://x\")": "'=HYPERLINK(\"http://x\")",
		"+1+1":                     "'+1+1",
		"-2":                       "'-2",
		"@SUM(A1)":                 "'@SUM(A1)",
		"\tcmd":                    "'\tcmd",
		"Main Gate":                "Main Gate",
		"":                         "",
	}
	for in, want := range cases {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
