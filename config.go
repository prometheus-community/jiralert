package jiralert

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
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
func LoadFile(filename string) (*Config, []byte, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := Load(string(content))
	if err != nil {
		return nil, nil, err
	}

	resolveFilepaths(filepath.Dir(filename), cfg)
	return cfg, content, nil
}

// resolveFilepaths joins all relative paths in a configuration
// with a given base directory.
func resolveFilepaths(baseDir string, cfg *Config) {
	join := func(fp string) string {
		if len(fp) > 0 && !filepath.IsAbs(fp) {
			fp = filepath.Join(baseDir, fp)
		}
		return fp
	}

	for i, tf := range cfg.Templates {
		cfg.Templates[i] = join(tf)
	}
}

var (
	// DefaultJiraConfig defines default values for Jira configurations.
	DefaultJiraConfig = JiraConfig{
		IssueType:   `Bug`,
		Priority:    `Critical`,
		Summary:     `{{ template "jira.default.summary" . }}`,
		Description: `{{ template "jira.default.description" . }}`,
		ReopenState: `To Do`,
	}
)

type JiraConfig struct {
	Name string `yaml:"name" json:"name"`

	// API access fields
	APIURL   string `yaml:"api_url" json:"api_url"`
	User     string `yaml:"user" json:"user"`
	Password Secret `yaml:"password" json:"password"`

	// Required issue fields
	Project     string `yaml:"project" json:"project"`
	IssueType   string `yaml:"issue_type" json:"issue_type"`
	Summary     string `yaml:"summary" json:"summary"`
	ReopenState string `yaml:"reopen_state" json:"reopen_state"`

	// Optional issue fields
	Priority          string                 `yaml:"priority" json:"priority"`
	Description       string                 `yaml:"description" json:"description"`
	WontFixResolution string                 `yaml:"wont_fix_resolution" json:"wont_fix_resolution"`
	Fields            map[string]interface{} `yaml:"fields" json:"fields"`

	// Catches all undefined fields and must be empty after parsing.
	XXX map[string]interface{} `yaml:",inline" json:"-"`
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (jc *JiraConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type plain JiraConfig
	if err := unmarshal((*plain)(jc)); err != nil {
		return err
	}
	return checkOverflow(jc.XXX, "receiver")
}

// Config is the top-level configuration for JIRAlert's config file.
type Config struct {
	Defaults  *JiraConfig   `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	Receivers []*JiraConfig `yaml:"receivers,omitempty" json:"receivers,omitempty"`
	Templates []string      `yaml:"templates" json:"templates"`

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
	type plain Config
	if err := unmarshal((*plain)(c)); err != nil {
		return err
	}

	// If a defaults block was open but empty, the defaults config is overwritten.
	// We have to restore it here.
	if c.Defaults == nil {
		c.Defaults = &JiraConfig{}
		*c.Defaults = DefaultJiraConfig
	}

	for _, jc := range c.Receivers {
		if jc.Name == "" {
			return fmt.Errorf("missing name for receiver %s", jc)
		}

		// Check API access fields
		if jc.APIURL == "" {
			if c.Defaults.APIURL == "" {
				return fmt.Errorf("missing api_url in receiver %s", jc.Name)
			}
			jc.APIURL = c.Defaults.APIURL
		}
		if jc.User == "" {
			if c.Defaults.User == "" {
				return fmt.Errorf("missing user in receiver %s", jc.Name)
			}
			jc.User = c.Defaults.User
		}
		if jc.Password == "" {
			if c.Defaults.Password == "" {
				return fmt.Errorf("missing password in receiver %s", jc.Name)
			}
			jc.Password = c.Defaults.Password
		}

		// Check required issue fields
		if jc.Project == "" {
			if c.Defaults.Project == "" {
				return fmt.Errorf("missing project in receiver %s", jc.Name)
			}
			jc.Project = c.Defaults.Project
		}
		if jc.IssueType == "" {
			if c.Defaults.IssueType == "" {
				return fmt.Errorf("missing issue_type in receiver %s", jc.Name)
			}
			jc.IssueType = c.Defaults.IssueType
		}
		if jc.Summary == "" {
			if c.Defaults.Summary == "" {
				return fmt.Errorf("missing summary in receiver %s", jc.Name)
			}
			jc.Summary = c.Defaults.Summary
		}
		if jc.ReopenState == "" {
			if c.Defaults.ReopenState == "" {
				return fmt.Errorf("missing reopen_state in receiver %s", jc.Name)
			}
			jc.ReopenState = c.Defaults.ReopenState
		}

		// Populate optional issue fields, where necessary
		if jc.Priority == "" && c.Defaults.Priority != "" {
			jc.Priority = c.Defaults.Priority
		}
		if jc.Description == "" && c.Defaults.Description != "" {
			jc.Description = c.Defaults.Description
		}
		if jc.WontFixResolution == "" && c.Defaults.WontFixResolution != "" {
			jc.WontFixResolution = c.Defaults.WontFixResolution
		}
		if len(c.Defaults.Fields) > 0 {
			for key, value := range c.Defaults.Fields {
				if _, ok := jc.Fields[key]; !ok {
					jc.Fields[key] = c.Defaults.Fields[key]
				}
			}
		}
	}

	if len(c.Receivers) == 0 {
		return fmt.Errorf("no receivers defined")
	}

	return checkOverflow(c.XXX, "config")
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
