package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/faiface/beep"
	"github.com/faiface/beep/effects"
	"github.com/faiface/beep/flac"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"github.com/faiface/beep/vorbis"
	"github.com/faiface/beep/wav"
)

type ReadSeekCloser struct {
	*bytes.Reader
}

func (r ReadSeekCloser) Close() error {
	return nil
}

type Player struct {
	audioData   []byte
	audioFormat string
	format      beep.Format
	ctrl        *beep.Ctrl
	playing     bool
	paused      bool
	streamer    beep.StreamSeekCloser
	done        chan bool
	volumeCtrl  *effects.Volume
}

func main() {
	myApp := app.NewWithID("com.hsueh.musicplayer")
	myWindow := myApp.NewWindow("fyne音乐播放器")

	err := speaker.Init(44100, 4410)
	if err != nil {
		dialog.ShowError(fmt.Errorf("初始化音频设备失败: %v", err), myWindow)
		return
	}

	player := &Player{}
	statusLabel := widget.NewLabel("未加载任何文件")
	progress := widget.NewSlider(0, 1)
	progress.Step = 1

	var playBtn, pauseBtn *widget.Button
	var songList *widget.List
	var songFiles []string

	// 当前播放索引
	currentIndex := -1

	resetPlayer := func() {
		speaker.Lock()
		println("重置播放器状态")
		player.paused = true
		player.playing = false
		if player.ctrl != nil {
			if player.ctrl.Paused == false {
				player.ctrl.Paused = true
			}
		}
		if player.streamer != nil {
			player.streamer.Close()
			player.streamer = nil
		}
		player.ctrl = nil
		player.format = beep.Format{}
		if player.done != nil {
			select {
			case <-player.done:
				// 已关闭
			default:
				close(player.done)
			}
			player.done = nil
		}
		defer speaker.Unlock()
	}

	resetUI := func() {
		playBtn.SetText("播放")
		pauseBtn.SetText("暂停")
		statusLabel.SetText("播放结束")
		progress.SetValue(0)
	}

	volumeSlider := widget.NewSlider(-5, 5)
	volumeSlider.Step = 0.1
	volumeSlider.Value = 0

	play := func() {
		if player.audioData == nil {
			statusLabel.SetText("请先选择歌曲")
			return
		}

		stream := ReadSeekCloser{bytes.NewReader(player.audioData)}
		var streamer beep.StreamSeekCloser
		var format beep.Format
		var err error

		switch player.audioFormat {
		case "mp3":
			streamer, format, err = mp3.Decode(stream)
		case "wav":
			streamer, format, err = wav.Decode(stream)
		case "flac":
			streamer, format, err = flac.Decode(stream)
		case "ogg":
			streamer, format, err = vorbis.Decode(stream)
		default:
			dialog.ShowError(fmt.Errorf("不支持的格式"), myWindow)
			return
		}
		if err != nil {
			dialog.ShowError(fmt.Errorf("解码失败: %v", err), myWindow)
			return
		}
		player.streamer = streamer

		resampled := beep.Resample(4, format.SampleRate, 44100, streamer)
		player.volumeCtrl = &effects.Volume{
			Streamer: resampled,
			Base:     2,
			Volume:   volumeSlider.Value,
			Silent:   false,
		}

		player.ctrl = &beep.Ctrl{
			Streamer: player.volumeCtrl,
			Paused:   false,
		}

		speaker.Lock()
		player.playing = true
		player.paused = false

		player.done = make(chan bool)
		speaker.Unlock()

		statusLabel.SetText("正在播放...")
		playBtn.SetText("播放中")
		pauseBtn.SetText("暂停")

		speaker.Play(beep.Seq(player.ctrl, beep.Callback(func() {
			player.done <- true
			streamer.Close()
		})))

		progress.Max = float64(streamer.Len())
		go func() {
			for player.playing && player.ctrl != nil {
				pos := player.streamer.Position()
				fyne.Do(func() {
					progress.SetValue(float64(pos))
				})
				time.Sleep(500 * time.Millisecond)
			}
		}()

		go func() {
			res := <-player.done
			// println("播放完成", res)
			if res == true {
				resetPlayer()
				// fyne.CurrentApp().SendNotification(&fyne.Notification{
				// 	Title:   "播放完成",
				// 	Content: "歌曲播放完毕",
				// })
				// fyne.CurrentApp().SendNotification(&fyne.Notification{
				// 	Title:   "提示",
				// 	Content: "可选下一首",
				// })
				fyne.Do(resetUI)
				// println("播放完成，自动播放下一首")
				// time.Sleep(500 * time.Millisecond)
				// 自动播放下一首
				fyne.Do(func() {
					if (currentIndex + 1) < len(songFiles) {
						currentIndex++
					} else {
						currentIndex = 0
					}
					songList.Select(currentIndex)
				})

			}
		}()
	}

	progress.OnChanged = func(v float64) {
		if player.ctrl != nil && player.playing {
			speaker.Lock()
			_ = player.streamer.Seek(int(v))
			speaker.Unlock()
		}
	}

	pause := func() {
		if player.ctrl == nil {
			return
		}

		speaker.Lock()
		player.paused = !player.paused
		player.ctrl.Paused = player.paused
		speaker.Unlock()

		if player.paused {
			pauseBtn.SetText("继续")
			statusLabel.SetText("已暂停")
		} else {
			pauseBtn.SetText("暂停")
			statusLabel.SetText("继续播放中")
		}
	}

	loadAudioFile := func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		player.audioData = data

		switch {
		case strings.HasSuffix(strings.ToLower(path), ".mp3"):
			player.audioFormat = "mp3"
		case strings.HasSuffix(strings.ToLower(path), ".wav"):
			player.audioFormat = "wav"
		case strings.HasSuffix(strings.ToLower(path), ".flac"):
			player.audioFormat = "flac"
		case strings.HasSuffix(strings.ToLower(path), ".ogg"):
			player.audioFormat = "ogg"
		default:
			dialog.ShowError(fmt.Errorf("不支持的音频格式: %s", strings.ToLower(path)), myWindow)
			return fmt.Errorf("不支持的格式")
		}
		statusLabel.SetText("已加载: " + filepath.Base(path))
		return nil
	}

	openFolder := func() {
		dialog.NewFolderOpen(func(dir fyne.ListableURI, err error) {
			if err != nil || dir == nil {
				return
			}
			files, err := dir.List()
			if err != nil {
				dialog.ShowError(fmt.Errorf("读取文件夹失败: %v", err), myWindow)
				return
			}

			songFiles = nil
			for _, f := range files {
				name := strings.ToLower(f.Name())
				if strings.HasSuffix(name, ".mp3") || strings.HasSuffix(name, ".wav") ||
					strings.HasSuffix(name, ".flac") || strings.HasSuffix(name, ".ogg") {
					songFiles = append(songFiles, f.Path())
				}
			}
			songList.Refresh()
			currentIndex = -1
		}, myWindow).Show()
	}

	playBtn = widget.NewButton("播放", play)
	pauseBtn = widget.NewButton("暂停", pause)
	openBtn := widget.NewButton("打开文件夹", openFolder)

	volumeLabel := widget.NewLabel("音量: 0 dB")
	volumeSlider.OnChanged = func(v float64) {
		volumeLabel.SetText(fmt.Sprintf("音量: %.1f dB", v))
		if player.volumeCtrl != nil {
			speaker.Lock()
			player.volumeCtrl.Volume = v
			speaker.Unlock()
		}
	}

	songList = widget.NewList(
		func() int { return len(songFiles) },
		func() fyne.CanvasObject {
			return widget.NewLabel("song")
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(filepath.Base(songFiles[i]))
		},
	)
	songList.OnSelected = func(id widget.ListItemID) {
		resetPlayer()
		currentIndex = id
		println("选中歌曲:", songFiles[id])
		println("选中歌曲currentIndex:", currentIndex)
		if err := loadAudioFile(songFiles[id]); err != nil {
			dialog.ShowError(err, myWindow)
			return
		}
		play()
	}

	prevBtn := widget.NewButton("上一首", func() {
		println("上一首按钮被点击")
		if currentIndex > 0 {
			resetPlayer()
			currentIndex--
			songList.Select(currentIndex)
		} else {
			resetPlayer()
			currentIndex = (len(songFiles) - 1)
			songList.Select(currentIndex)
		}
	})

	nextBtn := widget.NewButton("下一首", func() {
		if currentIndex < (len(songFiles) - 1) {
			resetPlayer()
			currentIndex++
			songList.Select(currentIndex)
		} else {
			resetPlayer()
			currentIndex = 0
			songList.Select(currentIndex)
		}
	})

	left := container.NewVSplit(songList, container.NewVBox(
		statusLabel,
		progress,
		container.NewHBox(volumeLabel, volumeSlider),
		container.NewHBox(prevBtn, playBtn, pauseBtn, nextBtn, openBtn),
	))
	left.SetOffset(0.6)

	myWindow.SetContent(left)
	myWindow.Resize(fyne.NewSize(800, 600))
	myWindow.ShowAndRun()
}
