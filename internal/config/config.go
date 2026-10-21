package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that unmarshals from strings like "15s".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// Listen holds the local addresses the balancer publishes.
type Listen struct {
	SOCKS5 string `yaml:"socks5"`
	HTTP   string `yaml:"http"`
	Status string `yaml:"status"`
}

// Check holds health-check parameters.
type Check struct {
	Interval      Duration `yaml:"interval"`
	Timeout       Duration `yaml:"timeout"`
	Required      int      `yaml:"required"`
	SwitchMargin  float64  `yaml:"switch_margin"`
	UpThreshold   int      `yaml:"up_threshold"`   // consecutive passes to mark healthy
	DownThreshold int      `yaml:"down_threshold"` // consecutive failures to mark down
	LatencyEWMA   float64  `yaml:"latency_ewma"`   // 0..1 smoothing; 0 uses the raw sample
	URLs          []string `yaml:"urls"`
}

// Upstream is one proxy endpoint (v2ray/xray SOCKS5 inbound).
type Upstream struct {
	Name     string `yaml:"name"`
	SOCKS5   string `yaml:"socks5"`
	Priority int    `yaml:"priority"` // lower value wins; only the best tier is used
	Weight   int    `yaml:"weight"`   // >1 makes an upstream look faster (default 1)
}

// Log holds logging options (output is stdout, suitable for journald).
type Log struct {
	Level  string `yaml:"level"`  // debug|info|warn|error
	Format string `yaml:"format"` // text|json
}

// Hook is a command executed when the active upstream changes.
type Hook struct {
	Command string   `yaml:"command"` // empty disables the hook
	Timeout Duration `yaml:"timeout"`
}

// Config is the full service configuration.
type Config struct {
	Listen    Listen     `yaml:"listen"`
	Log       Log        `yaml:"log"`
	Check     Check      `yaml:"check"`
	OnSwitch  Hook       `yaml:"on_switch"`
	Upstreams []Upstream `yaml:"upstreams"`
}

// Load reads, parses and validates the config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Listen.SOCKS5 == "" {
		c.Listen.SOCKS5 = "127.0.0.1:10800"
	}
	if c.Listen.HTTP == "" {
		c.Listen.HTTP = "127.0.0.1:10801"
	}
	if c.Listen.Status == "" {
		c.Listen.Status = "127.0.0.1:10802"
	}
	if c.Check.Interval == 0 {
		c.Check.Interval = Duration(15 * time.Second)
	}
	if c.Check.Timeout == 0 {
		c.Check.Timeout = Duration(5 * time.Second)
	}
	if c.Check.Required == 0 {
		c.Check.Required = 1
	}
	if c.Check.SwitchMargin == 0 {
		c.Check.SwitchMargin = 0.3
	}
	if c.Check.UpThreshold == 0 {
		c.Check.UpThreshold = 1
	}
	if c.Check.DownThreshold == 0 {
		c.Check.DownThreshold = 2
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "text"
	}
	if c.OnSwitch.Timeout == 0 {
		c.OnSwitch.Timeout = Duration(10 * time.Second)
	}
}

func (c *Config) validate() error {
	if len(c.Upstreams) == 0 {
		return fmt.Errorf("no upstreams configured")
	}
	if len(c.Check.URLs) == 0 {
		return fmt.Errorf("no check urls configured")
	}
	if c.Check.Required > len(c.Check.URLs) {
		return fmt.Errorf("check.required (%d) > number of urls (%d)", c.Check.Required, len(c.Check.URLs))
	}
	if c.Check.LatencyEWMA < 0 || c.Check.LatencyEWMA > 1 {
		return fmt.Errorf("check.latency_ewma must be in [0,1], got %v", c.Check.LatencyEWMA)
	}
	names := map[string]bool{}
	for i := range c.Upstreams {
		u := &c.Upstreams[i]
		if u.Name == "" {
			return fmt.Errorf("upstream #%d has empty name", i)
		}
		if u.SOCKS5 == "" {
			return fmt.Errorf("upstream %q has empty socks5 address", u.Name)
		}
		if names[u.Name] {
			return fmt.Errorf("duplicate upstream name %q", u.Name)
		}
		if u.Weight == 0 {
			u.Weight = 1
		}
		if u.Weight < 0 {
			return fmt.Errorf("upstream %q has negative weight", u.Name)
		}
		names[u.Name] = true
	}
	return nil
}
