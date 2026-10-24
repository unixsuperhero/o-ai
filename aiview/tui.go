package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type loadedMsg Inventory

type tuiModel struct {
	options       Options
	inventory     Inventory
	category      int
	rows          []Entry
	cursor        int
	width, height int
	loading       bool
	searching     bool
	expanded      bool
	picking       bool
	modelFilter   string
	notice        string
	query         textinput.Model
	viewport      viewport.Model
	picker        filepicker.Model
}

var titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
var selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6"))
var mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

func newTUI(o Options) tuiModel {
	q := textinput.New()
	q.Placeholder = "Filter names, IDs, descriptions…"
	q.Prompt = "/ "
	q.CharLimit = 200
	picker := filepicker.New()
	picker.CurrentDirectory = o.Project
	picker.DirAllowed = true
	picker.FileAllowed = true
	picker.ShowHidden = true
	return tuiModel{options: o, width: 100, height: 30, loading: true, query: q, viewport: viewport.New(60, 20), picker: picker}
}
func loadInventory(o Options) tea.Cmd { return func() tea.Msg { return loadedMsg(Collect(o)) } }
func (m tuiModel) Init() tea.Cmd      { return loadInventory(m.options) }
func runTUI(o Options) error {
	_, err := tea.NewProgram(newTUI(o), tea.WithAltScreen()).Run()
	return err
}

func (m *tuiModel) rebuild() {
	o := m.options
	o.Search = m.query.Value()
	m.rows = filterModel(m.inventory.Entries(categories[m.category], o), m.modelFilter)
	m.cursor = max(0, min(m.cursor, len(m.rows)-1))
	m.setDetail()
}
func (m *tuiModel) setDetail() {
	m.resizeViewport()
	content := "No matching entries. Press b to clear filters."
	if len(m.rows) > 0 {
		content = detail(m.rows[m.cursor].Value)
	}
	m.viewport.SetContent(ansi.Hardwrap(content, max(1, m.viewport.Width), true))
	m.viewport.GotoTop()
}
func (m *tuiModel) resizeViewport() {
	m.viewport.Width = max(10, m.width-m.listWidth()-4)
	if m.expanded {
		m.viewport.Width = max(10, m.width-2)
	}
	m.viewport.Height = max(3, m.height-8)
}
func (m tuiModel) listWidth() int { return min(50, max(24, m.width/3)) }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.query.Width = max(10, v.Width-6)
		m.picker.SetHeight(max(3, v.Height-8))
		m.setDetail()
		return m, nil
	case loadedMsg:
		m.inventory = Inventory(v)
		m.loading = false
		m.rebuild()
		return m, nil
	}
	if m.picking {
		if key, ok := msg.(tea.KeyMsg); ok {
			if key.String() == "esc" {
				m.picking = false
				return m, nil
			}
			if key.String() == " " {
				m.options.Project = m.picker.CurrentDirectory
				m.picking = false
				m.loading = true
				m.notice = ""
				return m, loadInventory(m.options)
			}
		}
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		if selected, path := m.picker.DidSelectFile(msg); selected {
			info, err := os.Stat(path)
			if err != nil {
				m.notice = "Cannot inspect selected path"
				return m, nil
			}
			if info.IsDir() {
				m.options.Project = path
				m.picking = false
				m.loading = true
				return m, loadInventory(m.options)
			}
			m.picking = false
			m.expanded = true
			m.resizeViewport()
			cfg, err := readConfig(path)
			if err != nil {
				m.notice = "Selected file is not readable JSON/JSONC, YAML, or TOML"
				m.expanded = false
				return m, nil
			}
			m.viewport.SetContent(ansi.Hardwrap(detail(Setting{Path: path, Scope: "selected file", Values: object(redact(cfg, ""))}), m.viewport.Width, true))
			m.viewport.GotoTop()
		}
		return m, cmd
	}
	if m.searching {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "enter", "esc":
				m.searching = false
				m.query.Blur()
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.query, cmd = m.query.Update(msg)
		m.cursor = 0
		m.rebuild()
		return m, cmd
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		k := key.String()
		if k == "q" {
			return m, tea.Quit
		}
		if m.loading {
			return m, nil
		}
		switch k {
		case "esc":
			m.expanded = false
			m.setDetail()
			return m, nil
		case "tab", "shift+tab":
			delta := 1
			if k == "shift+tab" {
				delta = -1
			}
			m.category = (m.category + delta + len(categories)) % len(categories)
			m.cursor = 0
			m.expanded = false
			m.modelFilter = ""
			m.rebuild()
			return m, nil
		case "/":
			m.searching = true
			m.query.Focus()
			return m, textinput.Blink
		case "r":
			m.loading = true
			m.notice = ""
			return m, loadInventory(m.options)
		case "o":
			m.picking = true
			m.picker.CurrentDirectory = m.options.Project
			return m, m.picker.Init()
		case "b":
			m.options.Harness = ""
			m.options.Provider = ""
			m.modelFilter = ""
			m.query.SetValue("")
			m.cursor = 0
			m.rebuild()
			return m, nil
		case "h":
			choices := []string{""}
			for _, h := range m.inventory.Harnesses {
				if h.Installed || h.Configured {
					choices = append(choices, h.ID)
				}
			}
			index := 0
			for i, h := range choices {
				if h == m.options.Harness {
					index = i
				}
			}
			m.options.Harness = choices[(index+1)%len(choices)]
			m.options.Provider = ""
			m.modelFilter = ""
			m.cursor = 0
			m.rebuild()
			return m, nil
		case "enter":
			m.expanded = !m.expanded
			m.setDetail()
			return m, nil
		case "s":
			if len(m.rows) > 0 {
				if model, ok := m.rows[m.cursor].Value.(Model); ok {
					if err := saveSelection(m.options, model.ID); err != nil {
						m.notice = err.Error()
					} else {
						m.notice = "Saved selection: " + model.ID
					}
				}
			}
			return m, nil
		case "m":
			if len(m.rows) > 0 {
				if provider, ok := m.rows[m.cursor].Value.(Provider); ok {
					m.options.Harness = provider.Harness
					m.options.Provider = provider.Slug
					m.category = 2
					m.cursor = 0
					m.expanded = false
					m.query.SetValue("")
					m.rebuild()
				}
			}
			return m, nil
		case "t":
			if len(m.rows) > 0 {
				if model, ok := m.rows[m.cursor].Value.(Model); ok {
					m.modelFilter = model.ID
					m.category = 3
					m.cursor = 0
					m.expanded = false
					m.query.SetValue("")
					m.rebuild()
				}
			}
			return m, nil
		case "w":
			m.expanded = true
			m.resizeViewport()
			var text strings.Builder
			printWarnings(&text, m.inventory)
			if text.Len() == 0 {
				text.WriteString("No collection warnings.")
			}
			m.viewport.SetContent(ansi.Hardwrap(text.String(), m.viewport.Width, true))
			m.viewport.GotoTop()
			return m, nil
		}
		if !m.expanded {
			switch k {
			case "up", "k":
				m.cursor = max(0, m.cursor-1)
			case "down", "j":
				m.cursor = min(max(0, len(m.rows)-1), m.cursor+1)
			case "home", "g":
				m.cursor = 0
			case "end", "G":
				m.cursor = max(0, len(m.rows)-1)
			case "pgdown":
				m.cursor = min(max(0, len(m.rows)-1), m.cursor+max(1, (m.height-8)/2))
			case "pgup":
				m.cursor = max(0, m.cursor-max(1, (m.height-8)/2))
			default:
				return m, nil
			}
			m.setDetail()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m tuiModel) View() string {
	if m.width < 55 || m.height < 13 {
		return "aiview needs at least 55 columns × 13 rows. Resize, or q to quit.\n"
	}
	header := titleStyle.Render(" AIVIEW ") + "  local AI inventory"
	project := mutedStyle.Render(ansi.Truncate("Project: "+m.options.Project, m.width-2, "…"))
	if m.loading {
		return header + "\n" + project + "\n\nReading harness settings and model metadata…\n\nq quit"
	}
	if m.picking {
		return header + "\nChoose project folder or inspect configuration file\n" + m.picker.View() + "\nspace use current folder · right open · enter select · esc cancel\n" + m.notice
	}
	tabs := make([]string, len(categories))
	for i, category := range categories {
		if i == m.category {
			tabs[i] = titleStyle.Render("[" + category + "]")
		} else {
			tabs[i] = category
		}
	}
	filters := "harness: all"
	if m.options.Harness != "" {
		filters = "harness: " + m.options.Harness
	}
	if m.options.Provider != "" {
		filters += " · provider: " + m.options.Provider
	}
	if m.modelFilter != "" {
		filters += " · model: " + m.modelFilter
	}
	search := m.query.View()
	if !m.searching && m.query.Value() == "" {
		search = mutedStyle.Render("/ search  ·  " + filters)
	}
	var body string
	if m.expanded {
		body = m.viewport.View()
	} else {
		width := m.listWidth()
		height := max(3, m.height-8)
		visible := max(1, height/2)
		start := max(0, m.cursor-visible+1)
		end := min(len(m.rows), start+visible)
		var lines []string
		for i := start; i < end; i++ {
			row := m.rows[i]
			label := "  " + row.Name
			if i == m.cursor {
				label = selectedStyle.Render(ansi.Truncate("› "+row.Name, width, "…"))
			} else {
				label = ansi.Truncate(label, width, "…")
			}
			lines = append(lines, label, mutedStyle.Render(ansi.Truncate("  "+row.Harness+" · "+row.Summary, width, "…")))
		}
		if len(lines) == 0 {
			lines = []string{"No matching entries"}
		}
		left := lipgloss.NewStyle().Width(width).Height(height).Render(strings.Join(lines, "\n"))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, " │ ", m.viewport.View())
	}
	warnings := len(m.inventory.Warnings)
	for _, h := range m.inventory.Harnesses {
		warnings += len(h.Warnings)
	}
	status := fmt.Sprintf("%d entries · %d warnings (w) · captured %s", len(m.rows), warnings, m.inventory.CapturedAt.Local().Format("15:04:05"))
	if m.notice != "" {
		status = m.notice
	}
	footer := "tab category · j/k move · enter detail · h harness · m models · t thinking"
	if m.expanded {
		footer = "↑/↓/pgup/pgdown scroll · esc back · s save model"
	}
	return strings.Join([]string{header, project, strings.Join(tabs, "  "), search, body, ansi.Truncate(status, m.width, "…"), footer, "/ search · b clear · r refresh · o files/folders · q quit"}, "\n")
}
