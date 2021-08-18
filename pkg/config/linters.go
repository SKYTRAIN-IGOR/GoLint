package config

type Linters struct {
	Enable     []string
	Disable    []string
	Anduril    bool
	EnableAll  bool `mapstructure:"enable-all"`
	DisableAll bool `mapstructure:"disable-all"`
	Fast       bool

	Presets []string
}
