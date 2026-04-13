package config_test

import (
	"context"
	"os"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
)

func TestConfigWriteReadRoundtrip(t *testing.T) {
	stateDir := t.TempDir()
	name := "test-demo"

	demo := &config.Demo{
		Name:          name,
		Repo:          "https://github.com/example/iiab",
		Branch:        "master",
		ImageSizeMB:   15000,
		VolatileMode:  "overlay",
		BuildOnDisk:   true,
		SkipInstall:   false,
		CleanupFailed: true,
		LocalVars:     "vars/local_vars_small.yml",
		Wildcard:      false,
		Description:   `A demo with "quotes" and back\slash`,
		BaseName:      "",
		Subdomain:     "test-demo",
		IP:            "10.99.0.1",
		Status:        "pending",
	}

	if err := demo.Write(stateDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	read, err := config.Read(context.Background(), stateDir, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if read.Name != demo.Name {
		t.Errorf("expected Name %q, got %q", demo.Name, read.Name)
	}
	if read.Repo != demo.Repo {
		t.Errorf("expected Repo %q, got %q", demo.Repo, read.Repo)
	}
	if read.Branch != demo.Branch {
		t.Errorf("expected Branch %q, got %q", demo.Branch, read.Branch)
	}
	if read.ImageSizeMB != demo.ImageSizeMB {
		t.Errorf("expected ImageSizeMB %d, got %d", demo.ImageSizeMB, read.ImageSizeMB)
	}
	if read.VolatileMode != demo.VolatileMode {
		t.Errorf("expected VolatileMode %q, got %q", demo.VolatileMode, read.VolatileMode)
	}
	if !read.BuildOnDisk {
		t.Error("expected BuildOnDisk to be true")
	}
	if read.SkipInstall {
		t.Error("expected SkipInstall to be false")
	}
	if !read.CleanupFailed {
		t.Error("expected CleanupFailed to be true")
	}
	if read.LocalVars != demo.LocalVars {
		t.Errorf("expected LocalVars %q, got %q", demo.LocalVars, read.LocalVars)
	}
	if read.Wildcard {
		t.Error("expected Wildcard to be false")
	}
	if read.Description != demo.Description {
		t.Errorf("expected Description %q, got %q", demo.Description, read.Description)
	}
	if read.BaseName != demo.BaseName {
		t.Errorf("expected BaseName %q, got %q", demo.BaseName, read.BaseName)
	}
	if read.Subdomain != demo.Subdomain {
		t.Errorf("expected Subdomain %q, got %q", demo.Subdomain, read.Subdomain)
	}
}

func TestConfigWriteReadSpecialChars(t *testing.T) {
	stateDir := t.TempDir()
	name := "special-chars"

	testCases := []struct {
		desc, repo, branch, localVars string
	}{
		{
			desc:      `Description with "double quotes"`,
			repo:      `https://github.com/user/repo`,
			branch:    `refs/pull/123/head`,
			localVars: `path/with"quote.yml`,
		},
		{
			desc:      `Back\slash in description`,
			repo:      `https://github.com/user/repo`,
			branch:    `main`,
			localVars: `vars/local_vars.yml`,
		},
		{
			desc:      `Mixed "quotes" and back\slash`,
			repo:      `https://github.com/user/repo`,
			branch:    `feature/add-support`,
			localVars: `vars/local_vars.yml`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			demo := &config.Demo{
				Name:        name,
				Repo:        tc.repo,
				Branch:      tc.branch,
				ImageSizeMB: 10000,
				Description: tc.desc,
				LocalVars:   tc.localVars,
				Subdomain:   name,
				Status:      "pending",
			}

			if err := demo.Write(stateDir); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			read, err := config.Read(context.Background(), stateDir, name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if read.Description != tc.desc {
				t.Errorf("expected Description %q, got %q", tc.desc, read.Description)
			}
			if read.Repo != tc.repo {
				t.Errorf("expected Repo %q, got %q", tc.repo, read.Repo)
			}
			if read.Branch != tc.branch {
				t.Errorf("expected Branch %q, got %q", tc.branch, read.Branch)
			}
			if read.LocalVars != tc.localVars {
				t.Errorf("expected LocalVars %q, got %q", tc.localVars, read.LocalVars)
			}
		})
	}
}

func TestConfigUnknownKeySilentlyIgnored(t *testing.T) {
	stateDir := t.TempDir()
	name := "typo-demo"

	// Write a config with a deliberate typo (TOML)
	demoDir := stateDir + "/active/" + name
	if err := os.MkdirAll(demoDir, 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content := `demo_name = "typo-demo"
iiab_repo = "https://github.com/example/iiab"
iiab_brnach = "master"
image_size_mb = 10000
`
	if err := os.WriteFile(demoDir+"/config", []byte(content), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	read, err := config.Read(context.Background(), stateDir, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Typo key should be silently ignored (branch stays empty)
	if read.Branch != "" {
		t.Errorf("expected empty Branch, got %q", read.Branch)
	}
	if read.Name != "typo-demo" {
		t.Errorf("expected Name 'typo-demo', got %q", read.Name)
	}
}
