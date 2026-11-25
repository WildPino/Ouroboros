package windows

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
)

// TaskName is the scheduled task that runs Ouroboros (DESIGN.md §4).
const TaskName = `\Ouroboros\Tick`

// TaskSpec describes the scheduled task.
type TaskSpec struct {
	Command      string        // absolute path of ouroboros.exe
	Arguments    string        // already quoted
	Interval     time.Duration // repetition interval, 1 minute to 31 days
	RunOnBattery bool          // start even when on battery power
	Start        time.Time     // first trigger; local wall-clock time is used
	Description  string
	Paused       bool // registered disabled, so that reinstalling keeps a pause
}

// TaskState is the registration state of a scheduled task.
type TaskState int

const (
	TaskMissing TaskState = iota
	TaskEnabled
	TaskDisabled
)

func (s TaskState) String() string {
	switch s {
	case TaskEnabled:
		return "enabled"
	case TaskDisabled:
		return "disabled"
	default:
		return "missing"
	}
}

// taskStateFromXML reads the state from "schtasks /Query /XML" output. The
// task-wide switch is Settings/Enabled, omitted when true; triggers have an
// Enabled of their own, which is not the one "schtasks /Change /DISABLE"
// sets. The output is searched as bytes because schtasks declares UTF-16
// but writes in the console code page.
func taskStateFromXML(out []byte) (TaskState, error) {
	s := string(out)
	start := strings.Index(s, "<Settings>")
	end := strings.Index(s, "</Settings>")
	if start < 0 || end < start {
		return TaskMissing, errors.New("schtasks /Query: no task settings in the output")
	}
	settings := s[start:end]
	if strings.Contains(strings.ReplaceAll(settings, " ", ""), "<Enabled>false</Enabled>") {
		return TaskDisabled, nil
	}
	return TaskEnabled, nil
}

// taskDocument mirrors the Task Scheduler 1.2 schema, in schema order.
type taskDocument struct {
	XMLName      xml.Name         `xml:"Task"`
	Version      string           `xml:"version,attr"`
	Xmlns        string           `xml:"xmlns,attr"`
	Registration taskRegistration `xml:"RegistrationInfo"`
	Triggers     taskTriggers     `xml:"Triggers"`
	Principals   taskPrincipals   `xml:"Principals"`
	Settings     taskSettings     `xml:"Settings"`
	Actions      taskActions      `xml:"Actions"`
}

type taskRegistration struct {
	Description string `xml:"Description"`
	URI         string `xml:"URI"`
}

type taskTriggers struct {
	Time taskTimeTrigger `xml:"TimeTrigger"`
}

type taskTimeTrigger struct {
	Repetition    taskRepetition `xml:"Repetition"`
	StartBoundary string         `xml:"StartBoundary"`
	Enabled       bool           `xml:"Enabled"`
}

type taskRepetition struct {
	Interval          string `xml:"Interval"`
	StopAtDurationEnd bool   `xml:"StopAtDurationEnd"`
}

type taskPrincipals struct {
	Principal taskPrincipal `xml:"Principal"`
}

type taskPrincipal struct {
	ID       string `xml:"id,attr"`
	UserID   string `xml:"UserId"`
	RunLevel string `xml:"RunLevel"`
}

type taskSettings struct {
	MultipleInstancesPolicy    string           `xml:"MultipleInstancesPolicy"`
	DisallowStartIfOnBatteries bool             `xml:"DisallowStartIfOnBatteries"`
	StopIfGoingOnBatteries     bool             `xml:"StopIfGoingOnBatteries"`
	AllowHardTerminate         bool             `xml:"AllowHardTerminate"`
	StartWhenAvailable         bool             `xml:"StartWhenAvailable"`
	RunOnlyIfNetworkAvailable  bool             `xml:"RunOnlyIfNetworkAvailable"`
	IdleSettings               taskIdleSettings `xml:"IdleSettings"`
	AllowStartOnDemand         bool             `xml:"AllowStartOnDemand"`
	Enabled                    bool             `xml:"Enabled"`
	Hidden                     bool             `xml:"Hidden"`
	RunOnlyIfIdle              bool             `xml:"RunOnlyIfIdle"`
	WakeToRun                  bool             `xml:"WakeToRun"`
	ExecutionTimeLimit         string           `xml:"ExecutionTimeLimit"`
	Priority                   int              `xml:"Priority"`
}

type taskIdleSettings struct {
	StopOnIdleEnd bool `xml:"StopOnIdleEnd"`
	RestartOnIdle bool `xml:"RestartOnIdle"`
}

type taskActions struct {
	Context string   `xml:"Context,attr"`
	Exec    taskExec `xml:"Exec"`
}

type taskExec struct {
	Command   string `xml:"Command"`
	Arguments string `xml:"Arguments,omitempty"`
}

// systemSID is the LocalSystem account.
const systemSID = "S-1-5-18"

// TaskXML returns the task definition as the UTF-16 document that
// schtasks /Create /XML expects.
func TaskXML(spec TaskSpec) ([]byte, error) {
	doc, err := taskXMLUTF8(spec)
	if err != nil {
		return nil, err
	}
	return encodeUTF16LE(strings.Replace(string(doc), `encoding="UTF-8"`, `encoding="UTF-16"`, 1)), nil
}

func taskXMLUTF8(spec TaskSpec) ([]byte, error) {
	if spec.Command == "" {
		return nil, errors.New("task command not set")
	}
	interval, err := isoDuration(spec.Interval)
	if err != nil {
		return nil, err
	}
	doc := taskDocument{
		Version:      "1.2",
		Xmlns:        "http://schemas.microsoft.com/windows/2004/02/mit/task",
		Registration: taskRegistration{Description: spec.Description, URI: TaskName},
		Triggers: taskTriggers{Time: taskTimeTrigger{
			// No duration: the repetition goes on indefinitely.
			Repetition:    taskRepetition{Interval: interval},
			StartBoundary: spec.Start.Format("2006-01-02T15:04:05"),
			Enabled:       true,
		}},
		Principals: taskPrincipals{Principal: taskPrincipal{ID: "Author", UserID: systemSID, RunLevel: "HighestAvailable"}},
		Settings: taskSettings{
			// The named mutex already excludes overlapping runs; ignoring
			// new instances avoids queueing them.
			MultipleInstancesPolicy:    "IgnoreNew",
			DisallowStartIfOnBatteries: !spec.RunOnBattery,
			// A running backup is never stopped by unplugging: restic
			// would be killed and the work lost.
			StopIfGoingOnBatteries: false,
			AllowHardTerminate:     true,
			// Catch up on runs missed while the computer was off.
			StartWhenAvailable: true,
			IdleSettings:       taskIdleSettings{},
			AllowStartOnDemand: true,
			Enabled:            !spec.Paused,
			// Backups may take days; restic timeouts bound hung processes.
			ExecutionTimeLimit: "PT0S",
			Priority:           7,
		},
		Actions: taskActions{Context: "Author", Exec: taskExec{Command: spec.Command, Arguments: spec.Arguments}},
	}
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(`<?xml version="1.0" encoding="UTF-8"?>`+"\n"), out...), nil
}

// isoDuration formats d as an ISO 8601 duration within the range Task
// Scheduler accepts for a repetition interval.
func isoDuration(d time.Duration) (string, error) {
	if d < time.Minute || d > 31*24*time.Hour || d%time.Minute != 0 {
		return "", fmt.Errorf("task interval %s must be whole minutes between 1m and 31d", d)
	}
	var b strings.Builder
	b.WriteString("P")
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	if days > 0 {
		fmt.Fprintf(&b, "%dD", days)
	}
	if d > 0 {
		b.WriteString("T")
		if h := d / time.Hour; h > 0 {
			fmt.Fprintf(&b, "%dH", h)
			d -= h * time.Hour
		}
		if m := d / time.Minute; m > 0 {
			fmt.Fprintf(&b, "%dM", m)
		}
	}
	return b.String(), nil
}

func encodeUTF16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 2, 2+2*len(u))
	out[0], out[1] = 0xFF, 0xFE
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}
