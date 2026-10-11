package main

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestExplorerSelectionAndResponsiveLayout(t *testing.T) {
	ops := []Operation{{Name: "Sync.AWS", Description: "Synchronize resources"}, {Name: "Users.list"}}
	m := newExplorer(ops, "staging", "https://example.com", "myapp", operationHistory{Favorites: map[string]bool{}})
	for _, width := range []int{40, 120} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m = next.(explorerModel)
		if !strings.Contains(m.View(), "staging") {
			t.Fatal("profile missing")
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	final := next.(explorerModel)
	if final.selected == nil || final.selected.Name != "Sync.AWS" {
		t.Fatalf("wrong selection: %#v", final.selected)
	}
}
func TestFavoritesAndHistoryNeverStoreInputs(t *testing.T) {
	h := operationHistory{Favorites: map[string]bool{"Users.list": true}}
	items := explorerItems([]Operation{{Name: "Sync.AWS"}, {Name: "Users.list"}}, h)
	if items[0].(operationItem).op.Name != "Users.list" {
		t.Fatal("favorites not first")
	}
	for i := 0; i < 20; i++ {
		h.remember(strings.Repeat("x", i+1))
	}
	if len(h.Recent) != 10 {
		t.Fatal("unbounded recent history")
	}
	h.remember(h.Recent[3])
	if len(h.Recent) != 10 {
		t.Fatal("duplicate recent entry")
	}
}
func TestExecutionCancellationRetainsInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := map[string]any{"name": "alice"}
	m := newExecution(ctx, cancel, Operation{Name: "Test.run"}, "https://example.com", "myapp test run", body)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(executionModel)
	if ctx.Err() == nil {
		t.Fatal("Escape did not cancel request")
	}
	if m.body["name"] != "alice" {
		t.Fatal("input lost")
	}
	next, _ = m.Update(executionDone{err: context.Canceled})
	m = next.(executionModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if next.(executionModel).action != "edit" {
		t.Fatal("cannot edit after cancellation")
	}
}
func TestResultSearchAndTable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m := newExecution(ctx, cancel, Operation{Name: "Test.run"}, "", "", nil)
	next, _ := m.Update(executionDone{data: []byte(`[{"name":"alice"},{"name":"bob"}]`)})
	m = next.(executionModel)
	m.format = "table"
	m.search.SetValue("bob")
	m.refresh()
	if !strings.Contains(m.viewport.View(), "bob") || strings.Contains(m.viewport.View(), "alice") {
		t.Fatalf("filter failed: %s", m.viewport.View())
	}
}
func TestCommandRedactsSecretsAndQuotesValues(t *testing.T) {
	op := Operation{Name: "Test.run", Input: map[string]any{"properties": map[string]any{"key": map[string]any{"writeOnly": true}, "name": map[string]any{"type": "string"}}}}
	command := operationCommand("myapp", "staging", op, map[string]any{"key": "sensitive", "name": "a'b"})
	if strings.Contains(command, "sensitive") || !strings.Contains(command, "[redacted]") || !strings.Contains(command, `'a'"'"'b'`) {
		t.Fatalf("unsafe command: %s", command)
	}
}
