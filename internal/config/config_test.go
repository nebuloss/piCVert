package config

import (
	"strings"
	"testing"
)

// Both ports are reachable by default.
//
// Worth a test rather than a comment, because the obvious safe-looking choice
// for the administration port — localhost — makes it unreachable in exactly
// the deployment this service is built for: inside a container, localhost is
// inside the container.
func TestBothPortsAreReachable(t *testing.T) {
	c := Defaults()
	if c.Listen != "0.0.0.0:3000" {
		t.Errorf("the public port defaults to %q — in a container, localhost "+
			"means inside the container and a port forward reaches nothing",
			c.Listen)
	}
	if c.Admin.Listen != "0.0.0.0:3001" {
		t.Errorf("the administration port defaults to %q — it has to be "+
			"reachable from the network to be any use in a container",
			c.Admin.Listen)
	}
}

// Both being reachable, a password is the one thing that must be decided — and
// the refusal has to say so in words somebody can act on rather than fail
// obscurely.
func TestTheDefaultsAskForAPassword(t *testing.T) {
	c := Defaults()
	err := c.check()
	if err == nil {
		t.Fatal("a reachable administration port started with no password")
	}
	for _, phrase := range []string{"picvert passwd", "admin.listen"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Errorf("the refusal does not mention %q:\n%v", phrase, err)
		}
	}
}

// A reachable administration port with no password is refused, because what
// protected that port was that nobody could reach it.
func TestAReachableAdminPortNeedsAPassword(t *testing.T) {
	for _, address := range []string{"0.0.0.0:3001", ":3001", "10.0.0.5:3001"} {
		c := Defaults()
		c.Admin.Listen = address
		if err := c.check(); err == nil {
			t.Errorf("%q was accepted with no password", address)
		}
	}
}

// The three ways out, all of which must work.
func TestTheWaysToSatisfyIt(t *testing.T) {
	hash, err := Hash("a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		with func(*Config)
	}{
		{"a password", func(c *Config) { c.Admin.Password = hash }},
		{"saying so knowingly", func(c *Config) { c.Admin.Open = true }},
		{"turning the port off", func(c *Config) { c.Admin.Listen = "" }},
		{"keeping it local", func(c *Config) { c.Admin.Listen = "127.0.0.1:3001" }},
		{"the environment supplying it", func(c *Config) { c.Admin.Password = hash }},
	}
	for _, one := range cases {
		c := Defaults()
		c.Admin.Listen = "0.0.0.0:3001"
		one.with(&c)
		if err := c.check(); err != nil {
			t.Errorf("%s was still refused: %v", one.name, err)
		}
	}
}

// localhost is recognised however it is spelt, and nothing else is.
func TestWhatCountsAsLocal(t *testing.T) {
	local := []string{"127.0.0.1:3001", "localhost:3001", "[::1]:3001"}
	for _, address := range local {
		if !isLoopback(address) {
			t.Errorf("%q was not recognised as local", address)
		}
	}
	reachable := []string{"0.0.0.0:3001", ":3001", "10.0.0.5:3001",
		"[::]:3001", "192.168.1.10:3001", "nonsense"}
	for _, address := range reachable {
		if isLoopback(address) {
			t.Errorf("%q was treated as local", address)
		}
	}
}

// A plaintext password where a hash belongs is refused, because it looks
// exactly like success: the service starts, the login works, and the password
// is sitting in a file that ends up in a backup.
func TestAPlaintextPasswordIsRefused(t *testing.T) {
	c := Defaults()
	c.Admin.Password = "hunter2"
	if err := c.check(); err == nil {
		t.Fatal("a plaintext password was accepted")
	}
}
