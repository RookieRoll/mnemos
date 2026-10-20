package installer

import (
	"reflect"
	"testing"
)

func TestCommandWordsRoundTripsPOSIXQuotedApostrophes(t *testing.T) {
	command := "'C:\\Users\\O'\\''Neil\\mnemos $build.exe' --config 'C:\\Users\\O'\\''Neil\\config $HOME`name' prewarm"
	got, ok := commandWordsForOS(command, false)
	if !ok {
		t.Fatalf("commandWords rejected generated shell command %q", command)
	}
	want := []string{`C:\Users\O'Neil\mnemos $build.exe`, "--config", "C:\\Users\\O'Neil\\config $HOME`name", "prewarm"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commandWords = %#v, want %#v", got, want)
	}
}

func TestCommandWordsPreservesWindowsPathSeparators(t *testing.T) {
	command := `"D:\Study\mnemos\mnemos.exe" --config "D:\Study\config with spaces.toml" prewarm`
	got, ok := commandWordsForOS(command, true)
	if !ok {
		t.Fatalf("commandWords rejected Windows command %q", command)
	}
	want := []string{`D:\Study\mnemos\mnemos.exe`, "--config", `D:\Study\config with spaces.toml`, "prewarm"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commandWords = %#v, want %#v", got, want)
	}
}
