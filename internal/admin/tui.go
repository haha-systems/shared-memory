package admin

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/xiy/memory-mcp/internal/store"
)

type tickMsg time.Time
type dashboardMsg struct {
	stats    store.Stats
	reqLogs  []store.MCPRequestLog
	memories []store.RecentMemory
	err      error
	duration time.Duration
}

type dashboardStore interface {
	Stats(ctx context.Context, now time.Time) (store.Stats, error)
	RecentMCPRequestLogs(ctx context.Context, limit int) ([]store.MCPRequestLog, error)
	RecentMemories(ctx context.Context, limit int) ([]store.RecentMemory, error)
}

type model struct {
	ctx           context.Context
	st            dashboardStore
	stats         store.Stats
	reqLogs       []store.MCPRequestLog
	memories      []store.RecentMemory
	lastErr       error
	lastTick      time.Time
	logLines      []string
	maxLogs       int
	requestsLimit int
	memoriesLimit int
	width         int
	height        int
}

// Run starts a lightweight local admin dashboard.
func Run(ctx context.Context, st dashboardStore) error {
	m := model{
		ctx:           ctx,
		st:            st,
		maxLogs:       10,
		requestsLimit: 8,
		memoriesLimit: 8,
	}
	m = m.appendLog("admin UI started")
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchDashboardCmd(m.ctx, m.st, m.requestsLimit, m.memoriesLimit), tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m = m.appendLog("received quit signal")
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tickMsg:
		m.lastTick = time.Time(msg)
		return m, tea.Batch(fetchDashboardCmd(m.ctx, m.st, m.requestsLimit, m.memoriesLimit), tickCmd())
	case dashboardMsg:
		m.lastErr = msg.err
		if msg.err == nil {
			m.stats = msg.stats
			m.reqLogs = msg.reqLogs
			m.memories = msg.memories
			m = m.appendLog(fmt.Sprintf(
				"refresh ok total=%d short=%d long=%d req=%d mem=%d (%s)",
				msg.stats.Total,
				msg.stats.Short,
				msg.stats.Long,
				len(msg.reqLogs),
				len(msg.memories),
				formatDuration(msg.duration),
			))
		} else {
			m = m.appendLog(fmt.Sprintf("refresh error: %v", msg.err))
		}
	}
	return m, nil
}

func (m model) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("memory-mcp admin")
	meta := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("q to quit • refresh every 2s")

	statsBody := m.renderStats()
	logBody := "(no log events yet)"
	if len(m.logLines) > 0 {
		logBody = strings.Join(m.logLines, "\n")
	}

	paneWidth := 54
	if m.width > 0 {
		paneWidth = max(38, (m.width-3)/2)
	}
	paneHeight := 9
	if m.height > 0 {
		paneHeight = max(8, (m.height-8)/2)
	}

	topRow := joinColumns(
		renderPane("Stats", statsBody, paneWidth, paneHeight),
		renderPane("General Logs", logBody, paneWidth, paneHeight),
	)
	bottomRow := joinColumns(
		renderPane("MCP Requests", formatRequestPane(m.reqLogs), paneWidth, paneHeight),
		renderPane("Recent Memories", formatRecentMemoriesPane(m.memories), paneWidth, paneHeight),
	)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		meta,
		"",
		topRow,
		bottomRow,
	)
}

func (m model) renderStats() string {
	body := fmt.Sprintf(
		"Total memories:  %d\nShort-term:      %d\nLong-term:       %d\nExpired (now):   %d\nLast refresh:    %s",
		m.stats.Total,
		m.stats.Short,
		m.stats.Long,
		m.stats.Expired,
		formatTime(m.lastTick),
	)
	if m.lastErr != nil {
		body += "\n\nLast error: " + truncateText(compactWhitespace(m.lastErr.Error()), 120)
	}
	return body
}

func fetchDashboardCmd(ctx context.Context, st dashboardStore, reqLimit, memLimit int) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		now := time.Now().UTC()
		s, err := st.Stats(ctx, now)
		if err != nil {
			return dashboardMsg{err: err, duration: time.Since(start)}
		}

		reqLogs, err := st.RecentMCPRequestLogs(ctx, reqLimit)
		if err != nil {
			return dashboardMsg{stats: s, err: err, duration: time.Since(start)}
		}

		memories, err := st.RecentMemories(ctx, memLimit)
		if err != nil {
			return dashboardMsg{stats: s, reqLogs: reqLogs, err: err, duration: time.Since(start)}
		}

		return dashboardMsg{
			stats:    s,
			reqLogs:  reqLogs,
			memories: memories,
			duration: time.Since(start),
		}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}

func (m model) appendLog(line string) model {
	if strings.TrimSpace(line) == "" {
		return m
	}
	entry := fmt.Sprintf("[%s] %s", time.Now().UTC().Format("15:04:05"), line)
	m.logLines = append(m.logLines, entry)
	if m.maxLogs <= 0 {
		m.maxLogs = 10
	}
	if len(m.logLines) > m.maxLogs {
		m.logLines = m.logLines[len(m.logLines)-m.maxLogs:]
	}
	return m
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return d.String()
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(10 * time.Millisecond).String()
}

func renderPane(title, body string, width, height int) string {
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2)
	if width > 0 {
		style = style.Width(width)
	}
	if height > 0 {
		style = style.Height(height)
	}
	return style.Render(title + "\n\n" + body)
}

func joinColumns(left, right string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func formatRequestPane(rows []store.MCPRequestLog) string {
	if len(rows) == 0 {
		return "(no MCP requests yet)"
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		method := strings.TrimSpace(row.Method)
		if row.ToolName != "" {
			method += ":" + strings.TrimSpace(row.ToolName)
		}
		status := "ok"
		if !row.Success {
			status = "err"
		}
		line := fmt.Sprintf(
			"[%s] %-3s %-24s %4dms",
			formatClock(row.CreatedAt),
			status,
			truncateText(method, 24),
			max(0, row.DurationMS),
		)
		if !row.Success && strings.TrimSpace(row.ErrorText) != "" {
			line += " " + truncateText(compactWhitespace(row.ErrorText), 52)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatRecentMemoriesPane(rows []store.RecentMemory) string {
	if len(rows) == 0 {
		return "(no memories yet)"
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		scope := "L"
		if row.Scope == "short" {
			scope = "S"
		}
		summary := truncateText(compactWhitespace(row.Summary), 68)
		line := fmt.Sprintf(
			"[%s] %s %s :: %s",
			formatClock(row.CreatedAt),
			scope,
			truncateText(row.Namespace, 20),
			summary,
		)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatClock(t time.Time) string {
	if t.IsZero() {
		return "--:--:--"
	}
	return t.UTC().Format("15:04:05")
}

func truncateText(s string, limit int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit <= 3 {
		return string(r[:limit])
	}
	return string(r[:limit-3]) + "..."
}

func compactWhitespace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
