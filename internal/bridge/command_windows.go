package bridge

import (
	"errors"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Start suspended so no child can escape before assignment to our kill-on-close
// job. If ownership cannot be established, fail before allowing execution.
func startOwnedCommand(cmd *exec.Cmd) (func(), error) {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	closeJob := func() { windows.CloseHandle(job) }
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		closeJob()
		return nil, e
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW, HideWindow: true}
	cmd.Cancel = func() error { _ = windows.TerminateJobObject(job, 1); return cmd.Process.Kill() }
	if e = cmd.Start(); e != nil {
		closeJob()
		return nil, e
	}
	process, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if e == nil {
		e = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if e == nil {
		e = resumeOwnedThread(uint32(cmd.Process.Pid))
	}
	if e != nil {
		cmd.Process.Kill()
		cmd.Wait()
		closeJob()
		return nil, e
	}
	return closeJob, nil
}
func resumeOwnedThread(pid uint32) error {
	snapshot, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for e = windows.Thread32First(snapshot, &entry); e == nil; e = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID == pid {
			thread, e := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if e != nil {
				return e
			}
			defer windows.CloseHandle(thread)
			_, e = windows.ResumeThread(thread)
			return e
		}
	}
	return errors.New("could not resume the owned command")
}
