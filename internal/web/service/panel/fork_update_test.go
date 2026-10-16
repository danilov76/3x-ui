package panel

import (
	"os"
	"strings"
	"testing"
)

func TestForkUpdaterEmbedded(t *testing.T) {
	path, err := downloadPanelUpdater()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "MHSanaei/3x-ui") || !strings.Contains(string(b), "danilov76/3x-ui") {
		t.Fatal("incorrect update source")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0700 {
		t.Fatal("updater permissions")
	}
}

func TestForkPatchVersions(t *testing.T) {
	for _, tt := range []struct {
		latest string
		want   bool
	}{
		{"tunnels-3.8.5-patch6", true}, {"tunnels-3.8.5-patch10", true},
		{"tunnels-3.8.5-patch4", false}, {forkReleaseTag, false}, {"v3.9.0", false},
	} {
		if newerForkRelease(tt.latest, forkReleaseTag) != tt.want {
			t.Fatal(tt.latest)
		}
	}
}
