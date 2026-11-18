package runner

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func exe(name string) string { return name + ".exe" }

// setupProcess hides the console window. Children are handled by a job object in afterStart.
func setupProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

// afterStart puts the listener in a job object set to kill everything inside when the
// job closes, so ending Letta also ends whatever it started (shells, servers).
// Children inherit the job automatically. Closing the returned cleanup kills them all.
func afterStart(cmd *exec.Cmd) (cleanup func()) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err == nil && cmd.Process != nil {
		h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
		if err == nil {
			_ = windows.AssignProcessToJobObject(job, h)
			windows.CloseHandle(h)
		}
	}
	return func() { windows.CloseHandle(job) }
}
