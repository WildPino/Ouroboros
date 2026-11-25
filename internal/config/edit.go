package config

import (
	"errors"
	"fmt"
	"strings"
)

// ErrJobNotFound is returned when an edit names a job the configuration
// does not have.
var ErrJobNotFound = errors.New("no such job")

// Edit loads the configuration at path, applies change and saves the result
// with Save. Nothing is written when change fails or the result is invalid.
func Edit(path string, change func(*Config) error) error {
	c, err := Load(path)
	if err != nil {
		return err
	}
	if err := change(c); err != nil {
		return err
	}
	return Save(path, c)
}

// SetJob adds j, or replaces the job with the same name (letter case
// ignored, as job names are unique regardless of case), keeping its place.
func (c *Config) SetJob(j Job) {
	for i := range c.Jobs {
		if strings.EqualFold(c.Jobs[i].Name, j.Name) {
			c.Jobs[i] = j
			return
		}
	}
	c.Jobs = append(c.Jobs, j)
}

// RemoveJob removes the named job. Its snapshots stay in the repository.
func (c *Config) RemoveJob(name string) error {
	for i := range c.Jobs {
		if strings.EqualFold(c.Jobs[i].Name, name) {
			c.Jobs = append(c.Jobs[:i], c.Jobs[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrJobNotFound, name)
}
