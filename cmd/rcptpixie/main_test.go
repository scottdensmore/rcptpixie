package main

import (
	"os"
	"testing"

	"github.com/scottdensmore/rcptpixie/version"
)

func TestMain(m *testing.M) {
	// Run tests without flag parsing
	os.Exit(m.Run())
}

func TestVersionString(t *testing.T) {
	v := version.Get()
	if v.String() == "" {
		t.Error("Version string should not be empty")
	}
}

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantModel   string
		wantVersion bool
		wantHelp    bool
		wantVerbose bool
	}{
		{
			name:        "Default flags",
			args:        []string{},
			wantModel:   "llama3.2",
			wantVersion: false,
			wantHelp:    false,
			wantVerbose: false,
		},
		{
			name:        "Custom model",
			args:        []string{"-model", "llama2"},
			wantModel:   "llama2",
			wantVersion: false,
			wantHelp:    false,
			wantVerbose: false,
		},
		{
			name:        "Version flag",
			args:        []string{"-version"},
			wantModel:   "llama3.2",
			wantVersion: true,
			wantHelp:    false,
			wantVerbose: false,
		},
		{
			name:        "Help flag",
			args:        []string{"-help"},
			wantModel:   "llama3.2",
			wantVersion: false,
			wantHelp:    true,
			wantVerbose: false,
		},
		{
			name:        "Verbose flag",
			args:        []string{"-verbose"},
			wantModel:   "llama3.2",
			wantVersion: false,
			wantHelp:    false,
			wantVerbose: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, version, help, verbose := parseFlags(tt.args)
			if model != tt.wantModel {
				t.Errorf("model = %v, want %v", model, tt.wantModel)
			}
			if version != tt.wantVersion {
				t.Errorf("version = %v, want %v", version, tt.wantVersion)
			}
			if help != tt.wantHelp {
				t.Errorf("help = %v, want %v", help, tt.wantHelp)
			}
			if verbose != tt.wantVerbose {
				t.Errorf("verbose = %v, want %v", verbose, tt.wantVerbose)
			}
		})
	}
}
