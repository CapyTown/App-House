package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"sync"
	"time"

	"androidfun/internal/history"
	"androidfun/internal/preferences"
	"androidfun/internal/scrcpy"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type mirrorSession interface {
	Wait() error
	Stop(context.Context) error
}

type mirrorState struct {
	window                             *mygo.Window
	geometry                           mirrorGeometry
	windowWidth, windowHeight          int
	resizing                           bool
	ctx                                context.Context
	cancel                             context.CancelFunc
	config                             scrcpy.Config
	device                             *scrcpy.Device
	session                            mirrorSession
	recorder                           *scrcpy.Session
	recordingStarted, recordingStopped time.Time
	name, metadata                     string
	frame                              *ui.Bitmap
	frameMu                            sync.Mutex
	latestFrame                        image.Image
	framePosted                        bool
	touchX, touchY                     int
	touchStart                         time.Time
	busy, stopping, recordingStopping  bool
	status                             string
	failed                             bool
	lastFile                           string
	lifecycleMu                        sync.Mutex
	closing                            bool
	work                               sync.WaitGroup
	cleanupOnce                        sync.Once
	cleanupDone                        chan struct{}
	cleanupErr                         error
}

func mirrorOptions(p preferences.MirrorOptions) scrcpy.Options {
	return scrcpy.Options{StayAwake: p.StayAwake, KeepActive: p.KeepActive, TurnScreenOff: p.TurnScreenOff,
		AlwaysOnTop: p.AlwaysOnTop, NoAudio: p.NoAudio, ShowTouches: p.ShowTouches, DisableScreensaver: p.DisableScreensaver,
		MaxSize: p.MaxSize, MaxFPS: p.MaxFPS, VideoBitRate: p.VideoBitRate}
}

func (a *app) canMirror() bool {
	return a.canMirrorDevice(a.serial)
}

func (a *app) openMirror() {
	a.openMirrorDevice(a.serial)
}

func (a *app) canMirrorDevice(serial string) bool {
	return !a.busy && !a.confirmOpen && a.client.Path != "" && a.deviceOnline(serial)
}

func (a *app) openMirrorDevice(serial string) {
	if !a.canMirrorDevice(serial) {
		return
	}
	if a.mirror != nil && a.mirror.stopping {
		return
	}
	if m := a.mirror; m != nil && m.window != nil && !m.window.IsDestroyed() {
		if m.config.Serial != serial {
			a.reopenMirrorDevice(m, serial)
			return
		}
		m.window.Restore()
		m.window.Show()
		m.window.Focus()
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	m := &mirrorState{ctx: ctx, cancel: cancel, busy: true, status: a.tr("正在启动投屏…"), cleanupDone: make(chan struct{})}
	m.name = serial
	for _, device := range a.launcherDevices() {
		if device.Serial == serial {
			m.name, m.metadata = deviceName(device), deviceMetadata(device)
			break
		}
	}
	m.config = scrcpy.Config{Path: a.config.Mirroring.Path, ADBPath: a.client.Path, Serial: serial, Options: mirrorOptions(a.config.Mirroring)}
	m.device = &scrcpy.Device{ADBPath: a.client.Path, Serial: serial}
	a.mirror = m
	m.geometry = mirrorGeometry{width: 9, height: 20, header: 56}
	m.windowWidth, m.windowHeight = 442, 820
	m.window = mygo.NewWindow(mygo.WindowOptions{Title: m.name + " · " + appName, Width: m.windowWidth, Height: m.windowHeight,
		UseContentSize: true, DisableMaximize: true, DisableFullScreen: true, TitleBarStyle: mygo.TitleBarHidden,
		AlwaysOnTop: a.config.Mirroring.AlwaysOnTop, Content: ui.View(func(c *ui.Context) { a.mirrorView(c, m) })})
	m.window.OnResize(m.resizeWindow)
	m.resizeWindow()
	m.window.OnClosed(func() {
		if a.mirror == m {
			a.closeMirror()
		}
	})
	window := a.win
	m.runWork(func() {
		session, startErr := scrcpy.StartStream(ctx, m.config, func(frame image.Image) { a.queueMirrorFrame(m, window, frame) })
		window.Update(func() {
			if a.mirror != m || ctx.Err() != nil {
				return
			}
			m.busy = false
			if startErr != nil {
				m.status, m.failed = startErr.Error(), true
				m.window.Invalidate()
				return
			}
			m.session = session
			m.status = a.tr("投屏中")
			m.window.Invalidate()
		})
		if session == nil {
			return
		}
		err := session.Wait()
		// A stopped stream also ends recording and pending device commands,
		// even while the app's event loop is busy.
		m.cancel()
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 8*time.Second)
		err = errors.Join(err, m.device.RestoreRotation(restoreCtx))
		restoreCancel()
		window.Update(func() {
			if a.mirror != m || m.stopping {
				return
			}
			m.session = nil
			m.busy = false
			if err != nil {
				m.status, m.failed = err.Error(), true
			} else {
				m.status = a.tr("投屏已结束")
			}
			a.stopRecording(m)
			m.window.Invalidate()
		})
	})
}

// Register work before shutdown can begin. In particular, process ownership and
// recording persistence must never depend on an event-loop callback running.
func (m *mirrorState) runWork(work func()) bool {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closing {
		return false
	}
	m.work.Go(work)
	return true
}

func (m *mirrorState) beginCleanup() {
	m.cleanupOnce.Do(func() {
		m.lifecycleMu.Lock()
		m.closing = true
		m.cancel()
		m.lifecycleMu.Unlock()
		go func() {
			m.work.Wait()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			m.cleanupErr = m.device.RestoreRotation(ctx)
			close(m.cleanupDone)
		}()
	})
}

func stopMirrorSession(s mirrorSession) error {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 16*time.Second)
	defer cancel()
	return s.Stop(ctx)
}

// Keep the closing state reachable until all process and history workers finish,
// so quitting immediately after closing the controller still waits for cleanup.
func (a *app) closeMirror() {
	m := a.mirror
	if m == nil || m.stopping {
		return
	}
	m.stopping = true
	m.beginCleanup()
	window := a.win
	go func() {
		<-m.cleanupDone
		window.Update(func() {
			if a.mirror == m {
				a.mirror = nil
			}
			if m.cleanupErr != nil {
				a.status, a.failed = m.cleanupErr.Error(), true
				a.invalidateWindows()
			}
		})
	}()
	if m.window != nil && !m.window.IsDestroyed() {
		m.window.Close()
	}
}

func (a *app) shutdownMirror() {
	m := a.mirror
	if m == nil {
		return
	}
	m.stopping = true
	m.beginCleanup()
	// These workers never wait for the UI thread. The sessions' cancellation path
	// requests graceful quit, then has a bounded forced-termination fallback.
	<-m.cleanupDone
}

func (a *app) restartMirror(m *mirrorState) {
	a.reopenMirrorDevice(m, m.config.Serial)
}

func (a *app) reopenMirrorDevice(m *mirrorState, serial string) {
	if a.mirror != m || m.stopping {
		return
	}
	a.closeMirror()
	window := a.win
	go func() {
		<-m.cleanupDone
		window.Update(func() {
			if a.mirror == m {
				a.mirror = nil
			}
			if a.ctx.Err() == nil {
				a.openMirrorDevice(serial)
			}
		})
	}()
}

func (a *app) mirrorCommand(m *mirrorState, command func(context.Context) error) {
	if m.busy || m.session == nil || m.ctx.Err() != nil {
		return
	}
	m.busy = true
	window := a.win
	m.runWork(func() {
		ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
		defer cancel()
		err := command(ctx)
		window.Update(func() {
			if a.mirror != m || m.stopping {
				return
			}
			m.busy = false
			m.failed = err != nil
			if err != nil {
				m.status = err.Error()
			} else {
				m.status = a.tr("投屏中")
			}
			m.window.Invalidate()
		})
	})
}

func (a *app) takeScreenshot(m *mirrorState) {
	if m.busy || m.session == nil || m.stopping {
		return
	}
	m.busy = true
	parent, window, tr, path, user := m.window, a.win, a.translator(), a.historyPath, a.userID
	options := a.config.Mirroring
	go func() {
		target, dialogErr := captureTarget(options, "Screenshot", ".png", mygo.SaveDialogOptions{Parent: parent, Title: tr("保存截屏"), Filters: []mygo.FileFilter{{Name: "PNG", Extensions: []string{"png"}}}})
		m.runWork(func() {
			err := dialogErr
			var entries []history.Entry
			var saveErr error
			if err == nil && target != "" {
				ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
				err = m.device.Screenshot(ctx, target)
				cancel()
				entry, e := recordCapture(path, m.config.Serial, user, "screenshot", target, err)
				entries, saveErr = []history.Entry{entry}, e
			}
			window.Update(func() {
				a.acceptHistory(entries, saveErr)
				if a.mirror != m || m.stopping {
					return
				}
				m.busy = false
				m.failed = err != nil || saveErr != nil
				if saveErr != nil {
					m.status = saveErr.Error()
				} else if err != nil {
					m.status = err.Error()
				} else if target != "" {
					m.status = tr("截屏已保存")
					m.lastFile = target
				}
				m.window.Invalidate()
				a.invalidateWindows()
			})
		})
	}()
}

func recordCapture(path, serial string, user int, action, target string, err error) (history.Entry, error) {
	result := operationResult{Package: target, OK: err == nil, Message: "成功"}
	if err != nil {
		result.Message = err.Error()
	}
	return recordOperation(path, serial, user, action, result)
}

func (a *app) startRecording(m *mirrorState) {
	if m.busy || m.recorder != nil || m.session == nil || m.ctx.Err() != nil {
		return
	}
	m.busy = true
	config := m.config
	config.Headless = true
	config.WindowX, config.WindowY = nil, nil
	config.WindowWidth, config.WindowHeight = 0, 0
	config.WindowTitle = ""
	parent, window, tr, path, user := m.window, a.win, a.translator(), a.historyPath, a.userID
	options := a.config.Mirroring
	go func() {
		target, dialogErr := captureTarget(options, "Recording", ".mp4", mygo.SaveDialogOptions{Parent: parent, Title: tr("保存录屏"), Filters: []mygo.FileFilter{{Name: "MP4", Extensions: []string{"mp4"}}, {Name: "Matroska", Extensions: []string{"mkv"}}}})
		m.runWork(func() {
			err := dialogErr
			var recorder *scrcpy.Session
			if err == nil && target != "" {
				config.RecordPath = target
				recorder, err = scrcpy.Start(m.ctx, config)
			}
			startErr := err
			started := time.Now()
			window.Update(func() {
				if a.mirror != m || m.stopping {
					return
				}
				m.busy = false
				m.failed = startErr != nil
				if startErr != nil {
					m.status = startErr.Error()
				} else if recorder != nil {
					m.recorder = recorder
					m.recordingStarted, m.recordingStopped = started, time.Time{}
					m.status = tr("正在录屏")
					// A mirror may have closed while the save dialog was open.
					if m.session == nil {
						a.stopRecording(m)
					}
				}
				m.window.Invalidate()
			})
			if target == "" || dialogErr != nil {
				return
			}
			if recorder != nil {
				err = recorder.Wait()
				if err == nil {
					if info, e := os.Stat(target); e != nil {
						err = e
					} else if info.Size() == 0 {
						err = fmt.Errorf("recording is empty")
					}
				}
			}
			// Persist even startup failures, and before dispatching any UI callback.
			entry, saveErr := recordCapture(path, config.Serial, user, "record", target, err)
			resultErr := err
			window.Update(func() {
				a.acceptHistory([]history.Entry{entry}, saveErr)
				if a.mirror != m || m.stopping {
					return
				}
				if m.recorder == recorder {
					m.recorder = nil
				}
				m.recordingStopping = false
				m.failed = resultErr != nil || saveErr != nil
				if saveErr != nil {
					m.status = saveErr.Error()
				} else if resultErr != nil {
					m.status = resultErr.Error()
				} else {
					m.status = tr("录屏已保存")
					m.lastFile = target
				}
				m.window.Invalidate()
				a.invalidateWindows()
			})
		})
	}()
}

func (a *app) stopRecording(m *mirrorState) {
	if m.recorder == nil || m.recordingStopping {
		return
	}
	m.recordingStopping = true
	m.recordingStopped = time.Now()
	m.status = a.tr("正在保存录屏…")
	recorder := m.recorder
	go stopMirrorSession(recorder)
	if m.window != nil && !m.window.IsDestroyed() {
		m.window.Invalidate()
	}
}
