// +build darwin dragonfly freebsd linux netbsd openbsd solaris

package executor

import (
	"fmt"
	"io"
	"os"
	"syscall"

	syslog "github.com/RackSec/srslog"

	"github.com/hashicorp/nomad/client/driver/logging"
)

func (e *UniversalExecutor) LaunchSyslogServer() (*SyslogServerState, error) {
	// Ensure the context has been set first
	if e.ctx == nil {
		return nil, fmt.Errorf("SetContext must be called before launching the Syslog Server")
	}

	e.syslogChan = make(chan *logging.SyslogMessage, 2048)
	l, err := e.getListener(e.ctx.PortLowerBound, e.ctx.PortUpperBound)
	if err != nil {
		return nil, err
	}
	e.logger.Printf("[DEBUG] syslog-server: launching syslog server on addr: %v", l.Addr().String())
	if err := e.configureLoggers(); err != nil {
		return nil, err
	}

	e.syslogServer = logging.NewSyslogServer(l, e.syslogChan, e.logger)
	go e.syslogServer.Start()
	go e.collectLogs(e.lre, e.lro)
	syslogAddr := fmt.Sprintf("%s://%s", l.Addr().Network(), l.Addr().String())
	return &SyslogServerState{Addr: syslogAddr}, nil
}

func (e *UniversalExecutor) collectLogs(we io.Writer, wo io.Writer) {
	for logParts := range e.syslogChan {
		// If the severity of the log line is err then we write to stderr
		// otherwise all messages go to stdout
		if logParts.Severity == syslog.LOG_ERR {
			e.lre.Write(logParts.Message)
			e.lre.Write([]byte{'\n'})
		} else {
			e.lro.Write(logParts.Message)
			e.lro.Write([]byte{'\n'})
		}
	}
}

// configure new process group for child process
func (e *UniversalExecutor) setNewProcessGroup() error {
	// We need to check that as build flags includes windows for this file
	if e.cmd.SysProcAttr == nil {
		e.cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	e.cmd.SysProcAttr.Setpgid = true
	return nil
}

// Cleanup any still hanging user processes
func (e *UniversalExecutor) cleanupChildProcesses(proc *os.Process) error {
	// If new process group was created upon command execution
	// we can kill the whole process group now to cleanup any leftovers.
	if e.cmd.SysProcAttr != nil && e.cmd.SysProcAttr.Setpgid {
		if err := syscall.Kill(-proc.Pid, syscall.SIGKILL); err != nil && err.Error() != noSuchProcessErr {
			return err
		}
		return nil
	} else {
		return proc.Kill()
	}
}

func (e *UniversalExecutor) shutdownProcess(proc *os.Process) error {
	// Set default kill signal, as some drivers don't support configurable
	// signals (such as rkt)
	var osSignal os.Signal
	if e.command.TaskKillSignal != nil {
		osSignal = e.command.TaskKillSignal
	} else {
		osSignal = os.Interrupt
	}

	if err := proc.Signal(osSignal); err != nil && err.Error() != finishedErr {
		return fmt.Errorf("executor.shutdown error: %v", err)
	}

	return nil
}
