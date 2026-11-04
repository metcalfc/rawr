package apps

import (
	"context"
	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

func (a *application) activateVM(ctx context.Context) {
	vm, ok := a.selectedVM()
	if !ok {
		a.message = "No existing VM is available to open."
		return
	}
	if _, err := services.SSHArgs(vm); err != nil {
		a.setError(err)
		return
	}
	var err error
	if a.callbacks.SwitchVM != nil {
		err = a.callbacks.SwitchVM(vm)
	} else {
		err = a.canvas.Suspend(func() error { return a.deps.ssh(ctx, vm) })
	}
	if err != nil {
		a.setError(err)
		return
	}
	a.context.VM = vm.Name
	a.message = "Opened " + vm.Name
}
func (a *application) linkVM() {
	vm, ok := a.selectedVM()
	if !ok {
		a.message = "No existing VM is available to link."
		return
	}
	if a.callbacks.LinkVM == nil {
		a.message = "Use rawr link PROJECT VM to save a project link."
		return
	}
	if err := a.callbacks.LinkVM(vm); err != nil {
		a.setError(err)
		return
	}
	a.context.VM = vm.Name
	a.message = "Linked " + vm.Name
}
func (a *application) selectedVM() (model.VM, bool) {
	if a.vmDetails != nil {
		return *a.vmDetails, true
	}
	if a.list.Selected < 0 || a.list.Selected >= len(a.vms) {
		return model.VM{}, false
	}
	return a.vms[a.list.Selected], true
}
func (a *application) openLinkedVM(ctx context.Context) {
	vm, ok := a.selectedVM()
	if !ok {
		a.message = "Select an existing VM first."
		return
	}
	u, err := services.BrowserURL(vm)
	if err == nil {
		err = a.deps.openURL(ctx, u)
	}
	a.setError(err)
}
func (a *application) vmHandle(ctx context.Context, event tcell.Event) bool {
	if e, ok := event.(*tcell.EventKey); ok {
		if e.Key() == tcell.KeyEscape || e.Rune() == 'q' {
			if a.vmDetails != nil {
				a.vmDetails = nil
				a.document.Offset = 0
				return false
			}
			return true
		}
		switch e.Rune() {
		case 'r':
			a.refresh(ctx)
			return false
		case 'b':
			a.openLinkedVM(ctx)
			return false
		case 'l':
			a.linkVM()
			return false
		case 's':
			a.activateVM(ctx)
			return false
		case 'i':
			if a.vmDetails != nil {
				a.vmDetails = nil
			} else if vm, ok := a.selectedVM(); ok {
				a.vmDetails = &vm
			}
			a.document.Offset = 0
			return false
		}
		if e.Key() == tcell.KeyEnter {
			a.activateVM(ctx)
			return false
		}
	}
	if a.vmDetails != nil {
		a.document.Handle(event, len(a.vmLines()), a.area().Height)
		return false
	}
	// A mouse press selects a resource. Connecting is an explicit Enter/s action.
	a.list.Handle(event, len(a.vms), a.area())
	return false
}
func (a *application) vmLines() []string {
	vm, ok := a.selectedVM()
	if !ok {
		return []string{"No VM selected."}
	}
	ssh, _ := services.SSHArgs(vm)
	dest := "(unavailable)"
	if len(ssh) > 0 {
		dest = ssh[len(ssh)-1]
	}
	browser, err := services.BrowserURL(vm)
	if err != nil {
		browser = "(invalid provider URL)"
	}
	return ui.WrapLines([]string{"VM · " + vm.Name, "", "Status: " + vm.Status, "SSH: " + dest, "Browser: " + browser, "", "Enter/s connects this VM in the main work pane.", "Previous work stays in native tabs.", "b opens this VM in your browser.", "Esc/q returns to the VM list."}, a.area().Width)
}
func (a *application) vmDraw(last int) {
	if a.vmDetails != nil {
		a.document.Draw(a.canvas, a.area(), a.vmLines())
		return
	}
	a.list.Draw(a.canvas, a.area(), len(a.vms), func(i int) string {
		vm := a.vms[i]
		marker := "  "
		if vm.Name == a.context.VM {
			marker = "● "
		}
		return marker + vm.Name + "  " + vm.Status
	})
	if len(a.vms) == 0 && !a.loader.Loading() {
		a.write(contentStart, 0, "No existing VMs found. Press r to refresh.", normalStyle)
	}
}
func (a *application) vmHints() string {
	if a.vmDetails != nil {
		return "Enter shell · ? help · b browser · i/Esc back"
	}
	hints := "Enter shell · ? help · i info · b browser · r refresh"
	if a.callbacks.LinkVM != nil {
		hints += " · l link"
	}
	return hints
}
