package server

import (
	"strings"
	"testing"
)

// comfyRunMessage is a stored assistant turn that ran the tool and produced one
// image plus one audio file, which is what a stock graph returns.
func comfyRunMessage(prompt, imgFile string) SessionMessage {
	return SessionMessage{
		Role:    "assistant",
		Content: "Here is the render. It came out flat, want me to try another seed?",
		ToolLog: []SessionToolEntry{{
			Name:     comfyToolName,
			Workflow: "SD Portrait",
			PromptID: "job-1",
			Media:    []ChatMedia{{File: imgFile, Kind: comfyMediaImage, Mime: "image/png"}, {File: "a/clip.wav", Kind: comfyMediaAudio}},
		}},
	}
}

// Without the replay, a restored session reads "here is the render, make it
// blue" with nothing to look at, and the model invents what it saw.
func TestBuildSessionMessagesReplaysComfyImages(t *testing.T) {
	withComfyMediaBase(t, func() {
		st := newChatSessionStore(t.TempDir())
		m, err := saveComfyMedia("sess", 0, "a.png", "", testPNG(t, 64, 64))
		if err != nil {
			t.Fatal(err)
		}
		sess := &ChatSession{
			Messages: []SessionMessage{
				{Role: "user", Content: "draw a cat"},
				comfyRunMessage("a cat", m.File),
				{Role: "user", Content: "make it blue"},
			},
		}
		msgs := st.buildSessionMessages(sess, sessionModelInfo{HasVision: true, CanTools: true})

		var withImage int
		for _, msg := range msgs {
			if len(msg.Images) > 0 {
				withImage++
				if msg.Role != "assistant" {
					t.Errorf("images landed on a %q message", msg.Role)
				}
			}
		}
		if withImage != 1 {
			t.Fatalf("%d messages carried an image, want exactly the one that announced it", withImage)
		}
	})
}

// A model without vision is sent the text only; attaching images it cannot read
// wastes the context window for nothing.
func TestBuildSessionMessagesSkipsReplayWithoutVision(t *testing.T) {
	withComfyMediaBase(t, func() {
		st := newChatSessionStore(t.TempDir())
		m, err := saveComfyMedia("sess", 0, "a.png", "", testPNG(t, 64, 64))
		if err != nil {
			t.Fatal(err)
		}
		sess := &ChatSession{Messages: []SessionMessage{
			{Role: "user", Content: "draw a cat"},
			comfyRunMessage("a cat", m.File),
		}}
		msgs := st.buildSessionMessages(sess, sessionModelInfo{CanTools: true})
		for _, msg := range msgs {
			if len(msg.Images) > 0 {
				t.Fatal("a model without vision was sent an image")
			}
		}
	})
}

// A user turn that carries its own image resets the budget, so the model looks
// at what it was just asked to correct rather than at the oldest render.
func TestBuildSessionMessagesUserImageResetsReplay(t *testing.T) {
	withComfyMediaBase(t, func() {
		st := newChatSessionStore(t.TempDir())
		m, err := saveComfyMedia("sess", 0, "a.png", "", testPNG(t, 64, 64))
		if err != nil {
			t.Fatal(err)
		}
		sess := &ChatSession{Messages: []SessionMessage{
			{Role: "user", Content: "draw a cat"},
			comfyRunMessage("a cat", m.File),
			{Role: "user", Content: "like this one but blue", Attach: []ChatAttach{
				{Kind: "image", Name: "ref.png", MimeType: "image/png", Data: "AAA"},
			}},
			{Role: "assistant", Content: "Sure, bluer."},
		}}
		msgs := st.buildSessionMessages(sess, sessionModelInfo{HasVision: true, CanTools: true})

		var replayedAfterUserImage bool
		sawUserImage := false
		for _, msg := range msgs {
			if msg.Role == "user" && len(msg.Images) > 0 {
				sawUserImage = true
				continue
			}
			if sawUserImage && msg.Role == "assistant" && len(msg.Images) > 0 {
				replayedAfterUserImage = true
			}
		}
		if replayedAfterUserImage {
			t.Error("an old render was replayed after the user supplied their own image")
		}
	})
}

// The budget is bounded so a long session does not re-read every render.
func TestSessionReplayComfyImagesRespectsBudget(t *testing.T) {
	withComfyMediaBase(t, func() {
		var msgs []SessionMessage
		for i := 0; i < sessionComfyReplayRuns+3; i++ {
			m, err := saveComfyMedia("sess", i, "a.png", "", testPNG(t, 32, 32))
			if err != nil {
				t.Fatal(err)
			}
			msgs = append(msgs, comfyRunMessage("x", m.File))
		}
		batches := sessionReplayComfyImages(msgs, true)
		if len(batches) != sessionComfyReplayRuns {
			t.Fatalf("replayed %d runs, want the budget of %d", len(batches), sessionComfyReplayRuns)
		}
		// Newest first is what the backwards walk produces before the reversal, so
		// the surviving runs must be the tail of the transcript.
		if len(batches) == 0 || len(batches[len(batches)-1]) == 0 {
			t.Fatal("empty batch replayed")
		}
	})
}

// Audio and video were never shown to the model, so replaying them would be a
// lie.
func TestSessionReplaySkipsNonImageMedia(t *testing.T) {
	msgs := []SessionMessage{{
		Role: "assistant",
		ToolLog: []SessionToolEntry{{
			Name:  comfyToolName,
			Media: []ChatMedia{{File: "a/clip.mp4", Kind: comfyMediaVideo}, {File: "a/clip.wav", Kind: comfyMediaAudio}},
		}},
	}}
	if b := sessionReplayComfyImages(msgs, true); len(b) != 0 {
		t.Fatalf("replayed %d batches from non-image media", len(b))
	}
}

// A tool entry for something else must not trigger a read.
func TestSessionReplayIgnoresOtherTools(t *testing.T) {
	msgs := []SessionMessage{{
		Role:    "assistant",
		ToolLog: []SessionToolEntry{{Name: "web_search"}},
	}}
	if b := sessionReplayComfyImages(msgs, true); len(b) != 0 {
		t.Fatalf("replayed %d batches from an unrelated tool", len(b))
	}
}

// The assistant turn keeps its text; the images are added to it rather than
// becoming a separate turn.
func TestBuildSessionMessagesKeepsRunTextWithImages(t *testing.T) {
	withComfyMediaBase(t, func() {
		st := newChatSessionStore(t.TempDir())
		m, err := saveComfyMedia("sess", 0, "a.png", "", testPNG(t, 32, 32))
		if err != nil {
			t.Fatal(err)
		}
		sess := &ChatSession{Messages: []SessionMessage{
			{Role: "user", Content: "draw"},
			comfyRunMessage("a cat", m.File),
		}}
		msgs := st.buildSessionMessages(sess, sessionModelInfo{HasVision: true})
		found := false
		for _, msg := range msgs {
			if len(msg.Images) == 0 {
				continue
			}
			found = true
			if !strings.Contains(msg.Content, "want me to try another seed") {
				t.Errorf("text lost when images were attached: %q", msg.Content)
			}
		}
		if !found {
			t.Fatal("no message carried the image")
		}
	})
}
