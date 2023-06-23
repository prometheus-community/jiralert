// Copyright 2017 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"

	"github.com/trivago/tgo/tcontainer"
	yaml "gopkg.in/yaml.v3"
)

// Secret is a string that must not be revealed on marshaling.
type Secret string

// MarshalYAML implements the yaml.Marshaler interface.
func (s Secret) MarshalYAML() (interface{}, error) {
	if s != "" {
		return "<secret>", nil
	}
	return nil, nil
}

// UnmarshalYAML implements the yaml.Unmarshaler interface for Secrets.
func (s *Secret) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type plain Secret
	return unmarshal((*plain)(s))
}

// Load parses the YAML input into a Config.
func Load(s string) (*Config, error) {
	cfg := &Config{}
	err := yaml.Unmarshal([]byte(s), cfg)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadFile parses the given YAML file into a Config.
func LoadFile(filename string, logger log.Logger) (*Config, []byte, error) {
	level.Info(logger).Log("msg", "loading configuration", "path", filename)
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, nil, err
	}

	content, err = substituteEnvVars(content, logger)
	if err != nil {
		return nil, nil, err
	}

	cfg, err := Load(string(content))
	if err != nil {
		return nil, nil, err
	}

	resolveFilepaths(filepath.Dir(filename), cfg, logger)
	return cfg, content, nil
}

// expand env variables $(var) from the config file
// taken from https://github.dev/thanos-io/thanos/blob/296c4ab4baf2c8dd6abdf2649b0660ac77505e63/pkg/reloader/reloader.go#L445-L462 by https://github.com/fabxc
func substituteEnvVars(b []byte, logger log.Logger) (r []byte, err error) {
	var envRe = regexp.MustCompile(`\$\(([a-zA-Z_0-9]+)\)`)
	r = envRe.ReplaceAllFunc(b, func(n []byte) []byte {
		if err != nil {
			return nil
		}

		n = n[2 : len(n)-1]

		v, ok := os.LookupEnv(string(n))
		if !ok {
			err = fmt.Errorf("Missing env variable: %q", n)
			return nil
		}
		return []byte(v)
	})
	return r, err
}

// resolveFilepaths joins all relative paths in a configuration
// with a given base directory.
func resolveFilepaths(baseDir string, cfg *Config, logger log.Logger) {
	join := func(fp string) string {
		if len(fp) == 0 || filepath.IsAbs(fp) {
			return fp
		}
		absFp := filepath.Join(baseDir, fp)
		level.Debug(logger).Log("msg", "resolved relative configuration path", "relativePath", fp, "absolutePath", absFp)
		return absFp
	}

	cfg.Template = join(cfg.Template)
}

// AutoResolve is the struct used for defining jira resolution state when alert is resolved.
type AutoResolve struct {
	State string `yaml:"state" json:"state"`
}

// ReceiverConfig is the configuration for one receiver. It has a unique name and includes API access fields (url and
// auth) and issue fields (required -- e.g. project, issue type -- and optional -- e.g. priority).
type ReceiverConfig struct {
	Name string `yaml:"name" json:"name"`

	// API access fields
	APIURL              string `yaml:"api_url" json:"api_url"`
	User                string `yaml:"user" json:"user"`
	Password            Secret `yaml:"password" json:"password"`
	PersonalAccessToken Secret `yaml:"personal_access_token" json:"personal_access_token"`

	// Required issue fields
	Project        string    `yaml:"project" json:"project"`
	IssueType      string    `yaml:"issue_type" json:"issue_type"`
	Summary        string    `yaml:"summary" json:"summary"`
	ReopenState    string    `yaml:"reopen_state" json:"reopen_state"`
	ReopenDuration *Duration `yaml:"reopen_duration" json:"reopen_duration"`

	// Optional issue fields
	Priority          string                 `yaml:"priority" json:"priority"`
	Description       string                 `yaml:"description" json:"description"`
	WontFixResolution string                 `yaml:"wont_fix_resolution" json:"wont_fix_resolution"`
	Fields            map[string]interface{} `yaml:"fields" json:"fields"`
	Components        []string               `yaml:"components" json:"components"`
	StaticLabels      []string               `yaml:"static_labels" json:"static_labels"`

	// Label copy settings
	AddGroupLabels bool `yaml:"add_group_labels" json:"add_group_labels"`

	// Flag to auto-resolve opened issue when the alert is resolved.
	UpdateInComment bool `yaml:"update_in_comment" json:"update_in_comment"`

	// Flag to auto-resolve opened issue when the alert is resolved.
	AutoResolve *AutoResolve `yaml:"auto_resolve" json:"auto_resolve"`

	// Catches all undefined fields and must be empty after parsing.
	XXX map[string]interface{} `yaml:",inline" json:"-"`
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (rc *ReceiverConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type plain ReceiverConfig
	if err := unmarshal((*plain)(rc)); err != nil {
		return err
	}
	// Recursively convert any maps to map[string]interface{}, filtering out all non-string keys, so the json encoder
	// doesn't blow up when marshaling JIRA requests.
	fieldsWithStringKeys, err := tcontainer.ConvertToMarshalMap(rc.Fields, func(v string) string { return v })
	if err != nil {
		return err
	}
	rc.Fields = fieldsWithStringKeys
	return checkOverflow(rc.XXX, "receiver")
}

// Config is the top-level configuration for JIRAlert's config file.
type Config struct {
	Defaults  *ReceiverConfig   `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	Receivers []*ReceiverConfig `yaml:"receivers,omitempty" json:"receivers,omitempty"`
	Template  string            `yaml:"template" json:"template"`

	// Catches all undefined fields and must be empty after parsing.
	XXX map[string]interface{} `yaml:",inline" json:"-"`
}

func (c Config) String() string {
	b, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Sprintf("<error creating config string: %s>", err)
	}
	return string(b)
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (c *Config) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// We want to set c to the defaults and then overwrite it with the input.
	// To make unmarshal fill the plain data struct rather than calling UnmarshalYAML
	// again, we have to hide it using a type indirection.

	// TODO: This function panics when there are no defaults. This needs to be fixed.

	type plain Config
	if err := unmarshal((*plain)(c)); err != nil {
		return err
	}

	if (c.Defaults.User != "" || c.Defaults.Password != "") && c.Defaults.PersonalAccessToken != "" {
		return fmt.Errorf("bad auth config in defaults section: user/password and PAT authentication are mutually exclusive")
	}

	if c.Defaults.AutoResolve != nil {
		if c.Defaults.AutoResolve.State == "" {
			return fmt.Errorf("bad config in defaults section: state cannot be empty")
		}
	}

	for _, rc := range c.Receivers {
		if rc.Name == "" {
			return fmt.Errorf("missing name for receiver %+v", rc)
		}

		// Check API access fields.
		if rc.APIURL == "" {
			if c.Defaults.APIURL == "" {
				return fmt.Errorf("missing api_url in receiver %q", rc.Name)
			}
			rc.APIURL = c.Defaults.APIURL
		}
		if _, err := url.Parse(rc.APIURL); err != nil {
			return fmt.Errorf("invalid api_url %q in receiver %q: %s", rc.APIURL, rc.Name, err)
		}

		if (rc.User != "" || rc.Password != "") && rc.PersonalAccessToken != "" {
			return fmt.Errorf("bad auth config in receiver %q: user/password and PAT authentication are mutually exclusive", rc.Name)
		}

		if (rc.User == "" || rc.Password == "") && rc.PersonalAccessToken == "" {
			if rc.User == "" && c.Defaults.User != "" {
				rc.User = c.Defaults.User
			}

			if rc.Password == "" && c.Defaults.Password != "" {
				rc.Password = c.Defaults.Password
			}

			if rc.User != "" && rc.Password != "" {
				// Nothing to do, we're ready to go with basic auth.
			} else if c.Defaults.PersonalAccessToken != "" {
				rc.PersonalAccessToken = c.Defaults.PersonalAccessToken
			} else {
				return fmt.Errorf("missing authentication in receiver %q", rc.Name)
			}
		}

		// Check required issue fields.
		if rc.Project == "" {
			if c.Defaults.Project == "" {
				return fmt.Errorf("missing project in receiver %q", rc.Name)
			}
			rc.Project = c.Defaults.Project
		}
		if rc.IssueType == "" {
			if c.Defaults.IssueType == "" {
				return fmt.Errorf("missing issue_type in receiver %q", rc.Name)
			}
			rc.IssueType = c.Defaults.IssueType
		}
		if rc.Summary == "" {
			if c.Defaults.Summary == "" {
				return fmt.Errorf("missing summary in receiver %q", rc.Name)
			}
			rc.Summary = c.Defaults.Summary
		}
		if rc.ReopenState == "" {
			if c.Defaults.ReopenState == "" {
				return fmt.Errorf("missing reopen_state in receiver %q", rc.Name)
			}
			rc.ReopenState = c.Defaults.ReopenState
		}
		if rc.ReopenDuration == nil {
			if c.Defaults.ReopenDuration == nil {
				return fmt.Errorf("missing reopen_duration in receiver %q", rc.Name)
			}
			rc.ReopenDuration = c.Defaults.ReopenDuration
		}

		// Populate optional issue fields, where necessary.
		if rc.Priority == "" && c.Defaults.Priority != "" {
			rc.Priority = c.Defaults.Priority
		}
		if rc.Description == "" && c.Defaults.Description != "" {
			rc.Description = c.Defaults.Description
		}
		if rc.WontFixResolution == "" && c.Defaults.WontFixResolution != "" {
			rc.WontFixResolution = c.Defaults.WontFixResolution
		}
		if rc.AutoResolve != nil {
			if rc.AutoResolve.State == "" {
				return fmt.Errorf("bad config in receiver %q, 'auto_resolve' was defined with empty 'state' field", rc.Name)
			}
		}
		if rc.AutoResolve == nil && c.Defaults.AutoResolve != nil {
			rc.AutoResolve = c.Defaults.AutoResolve
		}
		if len(c.Defaults.Fields) > 0 {
			for key, value := range c.Defaults.Fields {
				if _, ok := rc.Fields[key]; !ok {
					rc.Fields[key] = value
				}
			}
		}
		if len(c.Defaults.StaticLabels) > 0 {
			rc.StaticLabels = append(rc.StaticLabels, c.Defaults.StaticLabels...)
		}
    if ! rc.UpdateInComment {
      rc.UpdateInComment = c.Defaults.UpdateInComment
    }
	}

	if len(c.Receivers) == 0 {
		return fmt.Errorf("no receivers defined")
	}

	if c.Template == "" {
		return fmt.Errorf("missing template file")
	}

	return checkOverflow(c.XXX, "config")
}

// ReceiverByName loops the receiver list and returns the first instance with that name
func (c *Config) ReceiverByName(name string) *ReceiverConfig {
	for _, rc := range c.Receivers {
		if rc.Name == name {
			return rc
		}
	}
	return nil
}

func checkOverflow(m map[string]interface{}, ctx string) error {
	if len(m) > 0 {
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		return fmt.Errorf("unknown fields in %s: %s", ctx, strings.Join(keys, ", "))
	}
	return nil
}

type Duration time.Duration

var durationRE = regexp.MustCompile("^([0-9]+)(y|w|d|h|m|s|ms)$")

// ParseDuration parses a string into a time.Duration, assuming that a year
// always has 365d, a week always has 7d, and a day always has 24h.
func ParseDuration(durationStr string) (Duration, error) {
	matches := durationRE.FindStringSubmatch(durationStr)
	if len(matches) != 3 {
		return 0, fmt.Errorf("not a valid duration string: %q", durationStr)
	}
	var (
		n, _ = strconv.Atoi(matches[1])
		dur  = time.Duration(n) * time.Millisecond
	)
	switch unit := matches[2]; unit {
	case "y":
		dur *= 1000 * 60 * 60 * 24 * 365
	case "w":
		dur *= 1000 * 60 * 60 * 24 * 7
	case "d":
		dur *= 1000 * 60 * 60 * 24
	case "h":
		dur *= 1000 * 60 * 60
	case "m":
		dur *= 1000 * 60
	case "s":
		dur *= 1000
	case "ms":
		// Value already correct
	default:
		return 0, fmt.Errorf("invalid time unit in duration string: %q", unit)
	}
	return Duration(dur), nil
}

func (d Duration) String() string {
	var (
		ms   = int64(time.Duration(d) / time.Millisecond)
		unit = "ms"
	)
	if ms == 0 {
		return "0s"
	}
	factors := map[string]int64{
		"y":  1000 * 60 * 60 * 24 * 365,
		"w":  1000 * 60 * 60 * 24 * 7,
		"d":  1000 * 60 * 60 * 24,
		"h":  1000 * 60 * 60,
		"m":  1000 * 60,
		"s":  1000,
		"ms": 1,
	}

	switch int64(0) {
	case ms % factors["y"]:
		unit = "y"
	case ms % factors["w"]:
		unit = "w"
	case ms % factors["d"]:
		unit = "d"
	case ms % factors["h"]:
		unit = "h"
	case ms % factors["m"]:
		unit = "m"
	case ms % factors["s"]:
		unit = "s"
	}
	return fmt.Sprintf("%v%v", ms/factors[unit], unit)
}

func (d Duration) MarshalYAML() (interface{}, error) {
	return d.String(), nil
}

func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	dur, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(dur)
	return nil
}
