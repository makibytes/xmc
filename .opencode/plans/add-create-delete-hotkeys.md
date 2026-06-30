Plan: Add 'c' (create) and 'd' (delete) hotkeys to AI TUI sidebar object browser windows.

## Files to modify

### 1. `cmd/manage.go` (line 42-46)
Add `Create *ManageAction` and `Delete *ManageAction` to `ObjectType`:

```go
type ObjectType struct {
    Label        string
    Hierarchical bool
    List         func() ([]backends.ObjectNode, error)
    Create       *ManageAction  // optional: sidebar hotkey 'c'
    Delete       *ManageAction  // optional: sidebar hotkey 'd'
}
```

### 2. `cmd/aitui.go`

#### a) `objWindow` struct (line 223)
Add two fields:
```go
    createAction  *ManageAction
    deleteAction  *ManageAction
```

#### b) `aiTUIModel` struct (line 239)
Add prompt state for the create/delete flow:
```go
    // Sidebar inline prompt for create/delete
    promptActive  bool
    promptKind    string   // "create" or "delete"
    promptObjIdx  int      // objTypes index
    promptName    string   // typed name (create) or object name (delete)
```

#### c) `newAITUIModel` (line 387-393)
Propagate actions from ObjectType to objWindow:
```go
windows = append(windows, objWindow{
    label:         ot.Label,
    hierarchical:  ot.Hierarchical,
    listFn:        ot.List,
    createAction:  ot.Create,
    deleteAction:  ot.Delete,
})
```

#### d) `handleKey` (line 1320)
Add prompt interception at the top:
```go
func (m aiTUIModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    if m.promptActive {
        return m.handleKeyPrompt(msg)
    }
    switch m.state { ... }
}
```

#### e) New `handleKeyPrompt` function
- Create mode (`promptKind == "create"`): accumulate rune chars into `promptName`, Backspace removes last char, Enter calls `runManageAction(createAction, promptName)` then clears prompt, Esc clears prompt.
- Delete mode (`promptKind == "delete"`): Enter calls `runManageAction(deleteAction, promptName)` then clears prompt, Esc clears prompt.

#### f) New `runManageAction` helper
```go
func runManageAction(a *ManageAction, name string) error {
    if a == nil { return nil }
    if a.SetupFlags != nil {
        a.SetupFlags(&cobra.Command{})
    }
    return a.Run(name)
}
```

#### g) `handleKeyPane` (line 1821)
Add two cases after the `"x"` case (line 1879):
```go
case "c":
    if m.objTypes[wi].createAction != nil {
        m.promptActive = true
        m.promptKind = "create"
        m.promptObjIdx = wi
        m.promptName = ""
        return m, nil
    }
case "d":
    if m.objTypes[wi].deleteAction != nil {
        name := m.selectedName()
        if name != "" {
            m.promptActive = true
            m.promptKind = "delete"
            m.promptObjIdx = wi
            m.promptName = name
            return m, nil
        }
    }
```

#### h) `View()` (line 610)
After the status bar (line 617), when `promptActive`, render an inline prompt line:
```go
if m.promptActive {
    var promptLine string
    if m.promptKind == "create" {
        label := m.objTypes[m.promptObjIdx].label
        // Trim "s" plural suffix for display: "Queues" → "Queue"
        singleLabel := strings.TrimSuffix(label, "s")
        promptLine = fmt.Sprintf("Enter name for new %s: %s█", singleLabel, m.promptName)
    } else {
        promptLine = fmt.Sprintf("Delete \"%s\"? (Enter=confirm, Esc=cancel)", m.promptName)
    }
    return lipgloss.JoinVertical(lipgloss.Left,
        ...
        statusBar,
        dimStyle.Render(promptLine),
    )
}
```

### 3. Broker entry files

Update `Objects` lists to include `Create`/`Delete` action references.

| File | Label | Create | Delete |
|------|-------|--------|--------|
| `broker/artemis.go` | "Queues" | `spec.CreateQueue` | `spec.DeleteQueue` |
| | "Addresses" | `spec.CreateAddress` | `spec.DeleteAddress` |
| `broker/rabbitmq.go` | "Queues" | `spec.CreateQueue` | `spec.DeleteQueue` |
| | "Exchanges" | `spec.CreateExchange` | `spec.DeleteExchange` |
| `broker/kafka.go` | "Topics" | `spec.CreateTopic` | `spec.DeleteTopic` |
| | "Consumer Groups" | nil | nil |
| `broker/nats.go` | "Streams" | `spec.CreateQueue` | `spec.DeleteQueue` |
| `broker/redmc.go` | "Queues" | `spec.CreateQueue` | `spec.DeleteQueue` |
| | "Topics" | `spec.CreateTopic` | `spec.DeleteTopic` |
| `broker/pulsar.go` | "Topics" | `spec.CreateTopic` | `spec.DeleteTopic` |
| `broker/azmc.go` | "Queues" | `spec.CreateQueue` | `spec.DeleteQueue` |
| | "Topics" | `spec.CreateTopic` | `spec.DeleteTopic` |
| `broker/gmc.go` | "Queues" | `spec.CreateQueue` | `spec.DeleteQueue` |
| | "Topics" | `spec.CreateTopic` | `spec.DeleteTopic` |
| `broker/awsmc.go` | "Queues" | `spec.CreateQueue` | `spec.DeleteQueue` |
| | "Topics" | `spec.CreateTopic` | `spec.DeleteTopic` |
