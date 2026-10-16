package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/loopingz/webda.cli/tui"
	"github.com/spf13/cobra"
)

type operationHistory struct {
	Favorites map[string]bool `json:"favorites"`
	Recent    []string        `json:"recent"`
}

func historyPath(profile string) string { return filepath.Join(configDir(), profile+".history.json") }
func loadHistory(profile string) operationHistory {
	h := operationHistory{Favorites: map[string]bool{}}
	data, err := os.ReadFile(historyPath(profile))
	if err == nil {
		_ = json.Unmarshal(data, &h)
	}
	if h.Favorites == nil {
		h.Favorites = map[string]bool{}
	}
	return h
}
func saveHistory(profile string, h operationHistory) error {
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(historyPath(profile), data, 0o600)
}
func (h *operationHistory) remember(name string) {
	recent := []string{name}
	for _, entry := range h.Recent {
		if entry != name && len(recent) < 10 {
			recent = append(recent, entry)
		}
	}
	h.Recent = recent
}

type operationItem struct {
	op               Operation
	favorite, recent bool
}

func (i operationItem) Title() string {
	prefix := ""
	if i.favorite {
		prefix = "★ "
	} else if i.recent {
		prefix = "↺ "
	}
	return prefix + i.op.Name
}
func (i operationItem) Description() string { return i.op.Description }
func (i operationItem) FilterValue() string { return i.op.Name + " " + i.op.Description }
func explorerItems(ops []Operation, h operationHistory) []list.Item {
	items := make([]list.Item, 0, len(ops))
	for i := range ops {
		op := ops[i]
		recent := false
		for _, name := range h.Recent {
			if name == op.Name {
				recent = true
				break
			}
		}
		items = append(items, operationItem{op: op, favorite: h.Favorites[op.Name], recent: recent})
	}
	sort.SliceStable(items, func(a, b int) bool {
		i := items[a].(operationItem)
		j := items[b].(operationItem)
		if i.favorite != j.favorite {
			return i.favorite
		}
		if i.recent != j.recent {
			return i.recent
		}
		return i.op.Name < j.op.Name
	})
	return items
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func operationCommand(binary, profile string, op Operation, body map[string]any) string {
	command := shellQuote(binary) + " --profile " + shellQuote(profile) + " " + quotedSegments(op.Name)
	redacted := tui.RedactInput(op.Input, body)
	props, _ := op.Input["properties"].(map[string]any)
	for key := range redacted {
		if _, ok := props[key]; !ok {
			payload, _ := json.Marshal(redacted)
			return "printf '%s' " + shellQuote(string(payload)) + " | " + command + " --input - --no-interactive"
		}
	}
	for _, key := range sortedKeys(redacted) {
		v := redacted[key]
		var value string
		switch x := v.(type) {
		case string:
			value = x
		default:
			b, _ := json.Marshal(x)
			value = string(b)
		}
		command += " --" + schemaFlagName(key) + "=" + shellQuote(value)
	}
	return command
}
func quotedSegments(name string) string {
	parts := splitOperationName(name)
	for i, part := range parts {
		parts[i] = shellQuote(part)
	}
	return strings.Join(parts, " ")
}
func accent() lipgloss.Style {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "62", Dark: "111"})
}

type explorerModel struct {
	list                     list.Model
	ops                      []Operation
	profile, baseURL, binary string
	history                  operationHistory
	selected                 *Operation
	width, height            int
	notice                   string
}

func newExplorer(ops []Operation, profile, baseURL, binary string, h operationHistory) explorerModel {
	l := list.New(explorerItems(ops, h), list.NewDefaultDelegate(), 60, 20)
	l.Title = "Search operations"
	l.SetShowHelp(true)
	return explorerModel{list: l, ops: ops, profile: profile, baseURL: baseURL, binary: binary, history: h, width: 80, height: 24}
}
func (m explorerModel) Init() tea.Cmd { return nil }
func (m explorerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		w := msg.Width - 4
		if msg.Width >= 100 {
			w = (msg.Width - 6) / 2
		}
		height := msg.Height - 7
		if msg.Width < 100 {
			height = msg.Height - 13
		}
		m.list.SetSize(max(10, w), max(3, height))
	case tea.KeyMsg:
		if m.list.FilterState() != list.Filtering {
			switch msg.String() {
			case "enter":
				if item, ok := m.list.SelectedItem().(operationItem); ok {
					m.selected = &item.op
					return m, tea.Quit
				}
			case "f":
				if item, ok := m.list.SelectedItem().(operationItem); ok {
					m.history.Favorites[item.op.Name] = !m.history.Favorites[item.op.Name]
					cmd := m.list.SetItems(explorerItems(m.ops, m.history))
					return m, cmd
				}
			case "c":
				if item, ok := m.list.SelectedItem().(operationItem); ok {
					if err := clipboard.WriteAll(operationCommand(m.binary, m.profile, item.op, nil)); err != nil {
						m.notice = "Clipboard unavailable; command shown in details"
					} else {
						m.notice = "Command copied"
					}
				}
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}
func (m explorerModel) View() string {
	header := accent().Render(m.binary+" · "+m.profile) + "\n" + m.baseURL + "\n\n"
	details := "No operations match your search."
	if item, ok := m.list.SelectedItem().(operationItem); ok {
		var required, optional []string
		fields := tui.SchemaFields(item.op.Input)
		for i := range fields {
			field := &fields[i]
			if field.Required {
				required = append(required, field.Name)
			} else {
				optional = append(optional, field.Name)
			}
		}
		details = accent().Render(item.op.Name) + "\n\n" + item.op.Description + "\n\nRequired: " + strings.Join(required, ", ") + "\nOptional: " + strings.Join(optional, ", ") + "\n\n" + operationCommand(m.binary, m.profile, item.op, nil)
	}
	var content string
	if m.width >= 100 {
		w := (m.width - 6) / 2
		content = lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), "  ", lipgloss.NewStyle().Width(w).MaxHeight(max(5, m.height-7)).Render(details))
	} else {
		content = m.list.View() + "\n" + lipgloss.NewStyle().Width(max(10, m.width-4)).MaxHeight(5).Render(details)
	}
	return header + content + "\nEnter configure · / search · f favorite · c copy · q quit\n" + m.notice
}

func addExplorer(root *cobra.Command, profile, baseURL string, ops []Operation) {
	command := &cobra.Command{Use: "explore", Short: "Search, configure, and run operations interactively", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return runExplorer(cmd, profile, baseURL, ops) }}
	command.Flags().Bool("accessible", false, "use plain prompts suitable for screen readers")
	root.AddCommand(command)
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("unknown command %q", args[0])
		}
		if !terminalInput(cmd.InOrStdin()) || !terminalOutput(cmd.OutOrStdout()) {
			return cmd.Help()
		}
		return runExplorer(cmd, profile, baseURL, ops)
	}
}
func runExplorer(cmd *cobra.Command, profile, baseURL string, ops []Operation) error {
	if !terminalInput(cmd.InOrStdin()) || !terminalOutput(cmd.OutOrStdout()) {
		return fmt.Errorf("explore requires a terminal; use --help to list commands")
	}
	accessible, _ := cmd.Flags().GetBool("accessible")
	history := loadHistory(profile)
	for {
		var selected *Operation
		if accessible {
			options := []huh.Option[string]{huh.NewOption("Quit", "")}
			for _, item := range explorerItems(ops, history) {
				i := item.(operationItem)
				options = append(options, huh.NewOption(i.Title()+" — "+i.Description(), i.op.Name))
			}
			name := ""
			if err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title("Operations · " + profile).Options(options...).Value(&name))).WithAccessible(true).RunWithContext(cmd.Context()); err != nil {
				return err
			}
			if name == "" {
				return nil
			}
			for i := range ops {
				if ops[i].Name == name {
					o := ops[i]
					selected = &o
					break
				}
			}
		} else {
			model := newExplorer(ops, profile, baseURL, cmd.Root().Name(), history)
			result, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(cmd.Context())).Run()
			if err != nil {
				return err
			}
			final := result.(explorerModel)
			history = final.history
			if err := saveHistory(profile, history); err != nil {
				return err
			}
			selected = final.selected
			if selected == nil {
				return nil
			}
		}
		if selected == nil {
			return fmt.Errorf("selected operation is unavailable")
		}
		body := map[string]any{}
		for {
			fields := tui.SchemaFields(selected.Input)
			form, bindings := tui.BuildForm(fields, body)
			if form != nil {
				if err := form.WithAccessible(accessible).RunWithContext(cmd.Context()); err != nil {
					if cmd.Context().Err() != nil {
						return cmd.Context().Err()
					}
					break
				}
				normalized, err := tui.NormalizeForm(fields, body, bindings)
				if err != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
					continue
				}
				body = normalized
			}
			validated, err := tui.ValidateInput(selected.Input, body)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				continue
			}
			body = validated
			accepted, err := reviewOperation(cmd, *selected, profile, baseURL, body, accessible)
			if err != nil {
				return err
			}
			if !accepted {
				break
			}
			history.remember(selected.Name)
			if err := saveHistory(profile, history); err != nil {
				return err
			}
			timeout, _ := cmd.Flags().GetDuration("timeout")
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			action := "back"
			if accessible {
				data, runErr := executeOperation(ctx, *selected, cli, baseURL, body)
				if runErr != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), runErr)
				} else {
					rendered, _ := renderOutput(data, "pretty")
					_, _ = cmd.OutOrStdout().Write(rendered)
				}
				if err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title("Next action").Options(huh.NewOption("Back to operations", "back"), huh.NewOption("Edit inputs and run again", "edit")).Value(&action))).WithAccessible(true).RunWithContext(cmd.Context()); err != nil {
					cancel()
					return err
				}
			} else {
				model := newExecution(ctx, cancel, *selected, baseURL, operationCommand(cmd.Root().Name(), profile, *selected, body), body)
				result, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(cmd.Context())).Run()
				if err != nil {
					cancel()
					return err
				}
				action = result.(executionModel).action
			}
			cancel()
			if action == "quit" {
				return nil
			}
			if action != "edit" {
				break
			}
		}
	}
}
func reviewOperation(cmd *cobra.Command, op Operation, profile, baseURL string, body map[string]any, accessible bool) (bool, error) {
	payload, _ := json.MarshalIndent(tui.RedactInput(op.Input, body), "", "  ")
	title := "Run " + op.Name + " on " + profile + "?"
	if isDestructive(op) {
		title = "Destructive operation: " + title
	}
	accepted := false
	form := huh.NewForm(huh.NewGroup(huh.NewNote().Title("Review request").Description(baseURL+"\n"+string(payload)), huh.NewConfirm().Title(title).Affirmative("Run").Negative("Back").Value(&accepted)))
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		form = form.WithTheme(huh.ThemeBase())
	}
	err := form.WithAccessible(accessible).RunWithContext(cmd.Context())
	return accepted, err
}

type executionDone struct {
	data []byte
	err  error
}
type executionModel struct {
	ctx                    context.Context
	cancel                 context.CancelFunc
	op                     Operation
	baseURL, command       string
	body                   map[string]any
	started                time.Time
	elapsed                time.Duration
	spinner                spinner.Model
	viewport               viewport.Model
	search                 textinput.Model
	searching              bool
	done                   bool
	data                   []byte
	err                    error
	format, action, notice string
	width, height          int
}

func newExecution(ctx context.Context, cancel context.CancelFunc, op Operation, baseURL, command string, body map[string]any) executionModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	v := viewport.New(76, 16)
	search := textinput.New()
	search.Placeholder = "Search result"
	search.Prompt = "/ "
	return executionModel{ctx: ctx, cancel: cancel, op: op, baseURL: baseURL, command: command, body: body, started: time.Now(), spinner: s, viewport: v, search: search, format: "pretty", width: 80, height: 24}
}
func (m executionModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		data, err := executeOperation(m.ctx, m.op, cli, m.baseURL, m.body)
		return executionDone{data, err}
	})
}
func (m *executionModel) refresh() {
	content := ""
	if m.err != nil {
		content = m.err.Error()
	} else {
		data, err := renderOutput(m.data, m.format)
		if err != nil {
			content = err.Error()
		} else {
			content = string(data)
		}
	}
	if query := strings.ToLower(m.search.Value()); query != "" {
		var matches []string
		for _, line := range strings.Split(content, "\n") {
			if strings.Contains(strings.ToLower(line), query) {
				matches = append(matches, line)
			}
		}
		content = strings.Join(matches, "\n")
		if content == "" {
			content = "No matching lines"
		}
	}
	content = ansi.Strip(content)
	m.viewport.SetContent(content)
}
func (m executionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = max(10, msg.Width-4)
		m.viewport.Height = max(3, msg.Height-9)
	case executionDone:
		m.elapsed = time.Since(m.started)
		m.data = msg.data
		m.err = msg.err
		m.done = true
		m.refresh()
	case tea.KeyMsg:
		if m.searching {
			if msg.String() == "esc" || msg.String() == "enter" {
				m.searching = false
				m.search.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(msg)
			m.refresh()
			return m, cmd
		}
		switch msg.String() {
		case "ctrl+c", "q":
			m.cancel()
			m.action = "quit"
			return m, tea.Quit
		case "esc":
			if !m.done {
				m.cancel()
				m.notice = "Canceling request…"
			} else {
				m.action = "back"
				return m, tea.Quit
			}
		case "e":
			if m.done {
				m.action = "edit"
				return m, tea.Quit
			}
		case "j":
			if m.done {
				m.format = "pretty"
				m.refresh()
			}
		case "t":
			if m.done {
				m.format = "table"
				m.refresh()
			}
		case "/":
			if m.done {
				m.searching = true
				return m, m.search.Focus()
			}
		case "c":
			if err := clipboard.WriteAll(m.command); err != nil {
				m.notice = "Clipboard unavailable"
			} else {
				m.notice = "Redacted command copied"
			}
		}
	}
	var cmd tea.Cmd
	if !m.done {
		m.spinner, cmd = m.spinner.Update(msg)
	} else {
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}
func (m executionModel) View() string {
	status := "Running " + m.spinner.View() + " · " + time.Since(m.started).Round(time.Second).String()
	if m.done {
		if m.err != nil {
			status = "Request failed · edit inputs to retry"
		} else {
			status = "Completed in " + m.elapsed.Round(time.Millisecond).String() + " · " + m.format + " view"
		}
	}
	header := accent().Render(m.op.Name) + "\n" + m.baseURL + "\n" + status + "\n\n"
	content := m.viewport.View()
	if !m.done {
		content = "Esc cancels the local request. The server may already have started the operation."
	}
	return header + content + "\n" + m.search.View() + "\n↑↓ scroll · / search · t table · j JSON · e edit · c copy · Esc back · q quit\n" + m.notice
}
