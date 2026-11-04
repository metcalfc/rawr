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
	Number       int     `json:"number"`
	Title        string  `json:"title"`
	URL          string  `json:"url"`
	State        string  `json:"state"`
	Checks       []Check `json:"checks,omitempty"`
	ChecksError  string  `json:"checks_error,omitempty"`
	ChecksLoaded bool    `json:"checks_loaded,omitempty"`
}
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	State  string `json:"state"`
}
type GitHubStatus struct {
	State       string  `json:"state"`
	Repository  string  `json:"repository"`
	Branch      string  `json:"branch"`
	Message     string  `json:"message,omitempty"`
	PRs         []PR    `json:"prs"`
	Issues      []Issue `json:"issues"`
	Detached    bool    `json:"detached,omitempty"`
	Partial     bool    `json:"partial,omitempty"`
	PRsError    string  `json:"prs_error,omitempty"`
	IssuesError string  `json:"issues_error,omitempty"`
	FetchedAt   string  `json:"fetched_at,omitempty"`
}

type FileRequest struct {
	Path         string
	Diff, Staged bool
}

type Callbacks struct {
	SwitchVM   func(VM) error
	LinkVM     func(VM) error
	CheckoutPR func(PR) error
	OpenFile   func(FileRequest) error
}
