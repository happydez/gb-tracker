package config

import (
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration for YAML marshal/unmarshal
// as a human-readable string (e.g. "10s", "750ms").
type Duration time.Duration

func (d Duration) Unwrap() time.Duration {
	return time.Duration(d)
}

func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	v, err := time.ParseDuration(node.Value)
	if err != nil {
		return err
	}

	*d = Duration(v)

	return nil
}
