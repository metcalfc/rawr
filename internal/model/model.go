package model

import "github.com/metcalfc/rawr/rawrkit"

// Context shares the public Rawrkit launch contract.
type Context = rawrkit.Context

type VM struct {
	Name        string `json:"vm_name"`
	Status      string `json:"status"`
	SSHDest     string `json:"ssh_dest,omitempty"`
	HTTPSURL    string `json:"https_url,omitempty"`
	TerminalURL string `json:"terminal_url,omitempty"`
	ShelleyURL  string `json:"shelley_url,omitempty"`
}

type Check struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
	State  string `json:"state"`
	Link   string `json:"link"`
}
type PR struct {
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	State       string  `json:"state"`
	Checks      []Check `json:"checks,omitempty"`
	ChecksError string  `json:"checks_error,omitempty"`
}
type GitHubStatus struct {
	State      string `json:"state"`
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Message    string `json:"message,omitempty"`
	PRs        []PR   `json:"prs"`
}

type Callbacks struct {
	SwitchVM func(VM) error
	LinkVM   func(VM) error
}
