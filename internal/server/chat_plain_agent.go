package server

import (
	"context"
	"log"

	"strings"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

// runPlainChatLoop is the no-tools chat path: plain streaming completions plus
// image generation. Like the agent loops it writes through a chatSink, so the
// quick chat can stream straight to the browser while a detached session run
// persists the very same events.
func (s *Server) runPlainChatLoop(ctx context.Context, sink chatSink, body chatRequestBody) {
	send := sink.Send
	isImageGenerationModel := false
	if show, err := s.ollama.Show(ctx, body.Model); err == nil && show != nil {
		hasImage := false
		hasVision := false
		hasCompletion := false
		for _, cap := range show.Capabilities {
			switch cap {
			case "image":
				hasImage = true
			case "vision":
				hasVision = true
			case "completion":
				hasCompletion = true
			}
		}
		isImageGenerationModel = hasImage && !hasVision && !hasCompletion
	}

	isSameModelName := func(a, b string) bool {
		a = strings.TrimSpace(a)
		b = strings.TrimSpace(b)
		if a == b {
			return true
		}
		if strings.TrimSuffix(a, ":latest") == strings.TrimSuffix(b, ":latest") {
			return true
		}
		aBase := a[strings.LastIndex(a, "/")+1:]
		bBase := b[strings.LastIndex(b, "/")+1:]
		return strings.TrimSuffix(aBase, ":latest") == strings.TrimSuffix(bBase, ":latest")
	}

	isComputeOrOOMError := func(err error) bool {
		if err == nil {
			return false
		}
		s := strings.ToLower(err.Error())
		return strings.Contains(s, "compute error") ||
			strings.Contains(s, "out of memory") ||
			strings.Contains(s, "cuda error") ||
			strings.Contains(s, "cuda out of memory") ||
			strings.Contains(s, "metal: command buffer") ||
			strings.Contains(s, "failed to allocate") ||
			strings.Contains(s, "not enough memory") ||
			strings.Contains(s, "llama-server chat error")
	}

	isExternal := s.externalModels != nil && s.externalModels.IsExternal(body.Model)
	var wasCold bool = !isExternal
	if !isExternal {
		if running, err := s.ollama.PS(ctx); err == nil {
			for _, rm := range running {
				if isSameModelName(rm.Name, body.Model) || isSameModelName(rm.Model, body.Model) {
					wasCold = false
					break
				}
			}
		}
	}

	if isImageGenerationModel {
		var prompt string
		var images []string
		for i := len(body.Messages) - 1; i >= 0; i-- {
			msg := body.Messages[i]
			if msg.Role == "user" {
				prompt = msg.Content
				images = msg.Images
				break
			}
		}

		log.Printf("[image-gen] request: model=%s, prompt=%q, images_count=%d", body.Model, prompt, len(images))

		genReq := ollama.GenerateRequest{
			Model:   body.Model,
			Prompt:  prompt,
			Images:  images,
			Stream:  true,
			Options: body.Options,
			Width:   body.Width,
			Height:  body.Height,
			Steps:   body.Steps,
		}
		// Fallback: if root-level fields are zero, try reading from options for backward compatibility
		if genReq.Width == 0 {
			if v, ok := body.Options["width"]; ok {
				if vi, ok2 := v.(float64); ok2 {
					genReq.Width = int(vi)
				} else if vi, ok2 := v.(int); ok2 {
					genReq.Width = vi
				}
			}
		}
		if genReq.Height == 0 {
			if v, ok := body.Options["height"]; ok {
				if vi, ok2 := v.(float64); ok2 {
					genReq.Height = int(vi)
				} else if vi, ok2 := v.(int); ok2 {
					genReq.Height = vi
				}
			}
		}
		if genReq.Steps == 0 {
			if v, ok := body.Options["steps"]; ok {
				if vi, ok2 := v.(float64); ok2 {
					genReq.Steps = int(vi)
				} else if vi, ok2 := v.(int); ok2 {
					genReq.Steps = vi
				}
			}
		}
		startedAt := time.Now()
		var firstTokenTime time.Duration
		var final ollama.GenerateChunk
		var accContent strings.Builder
		err := s.ollama.Generate(ctx, genReq, func(chunk ollama.GenerateChunk) error {
			if wasCold && firstTokenTime == 0 && (chunk.Response != "" || chunk.Image != "") {
				firstTokenTime = time.Since(startedAt)
				s.recordModelColdLoad(body.Model, firstTokenTime.Milliseconds(), time.Now())
			}
			if chunk.Response != "" {
				accContent.WriteString(chunk.Response)
			}
			chatChunk := ollama.ChatChunk{
				Model:     chunk.Model,
				CreatedAt: chunk.CreatedAt,
				Done:      chunk.Done,
				Completed: chunk.Completed,
				Total:     chunk.Total,
			}
			content := chunk.Response
			if content == "" && chunk.Image != "" {
				content = chunk.Image
			}
			if content != "" {
				chatChunk.Message = ollama.ChatMessage{
					Role:    "assistant",
					Content: content,
				}
			}
			send("chunk", chatChunk)
			if chunk.Done {
				final = chunk
			}
			return nil
		})
		if err != nil && isComputeOrOOMError(err) && ctx.Err() == nil {
			log.Printf("[image-gen] compute/memory error detected for %s (%v), unloading models to free GPU/RAM and retrying once...", body.Model, err)
			if running, psErr := s.ollama.PS(ctx); psErr == nil {
				for _, rm := range running {
					_ = s.ollama.Unload(ctx, rm.Name)
				}
			}
			time.Sleep(600 * time.Millisecond)

			firstTokenTime = 0
			final = ollama.GenerateChunk{}
			accContent.Reset()
			err = s.ollama.Generate(ctx, genReq, func(chunk ollama.GenerateChunk) error {
				if firstTokenTime == 0 && (chunk.Response != "" || chunk.Image != "") {
					firstTokenTime = time.Since(startedAt)
				}
				if chunk.Response != "" {
					accContent.WriteString(chunk.Response)
				}
				chatChunk := ollama.ChatChunk{
					Model:     chunk.Model,
					CreatedAt: chunk.CreatedAt,
					Done:      chunk.Done,
					Completed: chunk.Completed,
					Total:     chunk.Total,
				}
				content := chunk.Response
				if content == "" && chunk.Image != "" {
					content = chunk.Image
				}
				if content != "" {
					chatChunk.Message = ollama.ChatMessage{
						Role:    "assistant",
						Content: content,
					}
				}
				send("chunk", chatChunk)
				if chunk.Done {
					final = chunk
				}
				return nil
			})
		}
		if err != nil {
			if ctx.Err() != nil {
				s.recordCancelUsage(body.Model, accContent.String(), startedAt)
				return
			}
			errMsg := err.Error()
			if strings.Contains(errMsg, "mlx runner failed") || strings.Contains(errMsg, "failed to initialize MLX") || strings.Contains(errMsg, "failed to load MLX") {
				if s.cfg.Language == "es" {
					errMsg = "El modelo de generación de imágenes no está soportado en este sistema operativo (Windows/Linux). Los modelos basados en MLX solo funcionan de forma nativa en dispositivos Apple Silicon (macOS)."
				} else {
					errMsg = "This image generation model is not supported on this operating system (Windows/Linux). MLX-based models only run natively on Apple Silicon (macOS) devices."
				}
			} else if isComputeOrOOMError(err) {
				if s.cfg.Language == "es" {
					errMsg = "El modelo se quedó sin memoria suficiente (VRAM / RAM) para procesar esta solicitud. Se intentó liberar memoria descargando procesos de modelos en segundo plano, pero la GPU/sistema no pudo procesar el contexto. Prueba reduciendo el tamaño del contexto (num_ctx), cerrando aplicaciones pesadas o usando una cuantización menor."
				} else {
					errMsg = "The model ran out of memory (VRAM / RAM) to process this request. An automatic attempt was made to free memory by unloading running models, but the GPU/system could not allocate sufficient memory. Try reducing the context length (num_ctx), closing heavy applications, or using a smaller quantization."
				}
			}
			send("error", map[string]any{"error": errMsg})
			return
		}

		evalCount := final.EvalCount
		evalDuration := final.EvalDuration
		promptEvalCount := final.PromptEvalCount
		if evalCount <= 0 {
			if est := estimateTextTokens(accContent.String()); est > 0 {
				evalCount = est
			}
		}
		if promptEvalCount <= 0 {
			if est := estimatePromptTokens(body); est > 0 {
				promptEvalCount = est
			}
		}
		if evalCount > 0 && evalDuration <= 0 {
			evalDuration = int64(time.Since(startedAt))
		}
		totalTokens := promptEvalCount + evalCount
		s.recordModelUsage(body.Model, evalCount, evalDuration, promptEvalCount, time.Now())
		send("done", map[string]any{
			"elapsed_ms":         time.Since(startedAt).Milliseconds(),
			"prompt_tokens":      promptEvalCount,
			"completion_tokens":  evalCount,
			"total_tokens":       totalTokens,
			"prompt_duration_ns": final.PromptEvalDuration,
			"eval_duration_ns":   evalDuration,
			"total_duration_ns":  final.TotalDuration,
			"done_reason":        final.DoneReason,
		})
		return
	}

	chatReq := ollama.ChatRequest{
		Model:    body.Model,
		Messages: body.Messages,
		Stream:   true,
		Think:    body.Think,
		Options:  body.Options,
	}

	startedAt := time.Now()
	var firstTokenTime time.Duration
	var final ollama.ChatChunk
	var accContent strings.Builder
	var accThinking strings.Builder
	err := s.chatWithModel(ctx, chatReq, func(chunk ollama.ChatChunk) error {
		if wasCold && firstTokenTime == 0 && (chunk.Message.Content != "" || chunk.Message.Thinking != "") {
			firstTokenTime = time.Since(startedAt)
			s.recordModelColdLoad(body.Model, firstTokenTime.Milliseconds(), time.Now())
		}
		if chunk.Message.Content != "" {
			accContent.WriteString(chunk.Message.Content)
		}
		if chunk.Message.Thinking != "" {
			accThinking.WriteString(chunk.Message.Thinking)
		}
		send("chunk", chunk)
		if chunk.Done {
			final = chunk
		}
		return nil
	})

	if err != nil && isComputeOrOOMError(err) && ctx.Err() == nil {
		log.Printf("[chat] compute/memory error detected for %s (%v), unloading models to free GPU/RAM and retrying once...", body.Model, err)
		if running, psErr := s.ollama.PS(ctx); psErr == nil {
			for _, rm := range running {
				_ = s.ollama.Unload(ctx, rm.Name)
			}
		}
		time.Sleep(600 * time.Millisecond)

		firstTokenTime = 0
		final = ollama.ChatChunk{}
		accContent.Reset()
		accThinking.Reset()
		err = s.chatWithModel(ctx, chatReq, func(chunk ollama.ChatChunk) error {
			if wasCold && firstTokenTime == 0 && (chunk.Message.Content != "" || chunk.Message.Thinking != "") {
				firstTokenTime = time.Since(startedAt)
				s.recordModelColdLoad(body.Model, firstTokenTime.Milliseconds(), time.Now())
			}
			if chunk.Message.Content != "" {
				accContent.WriteString(chunk.Message.Content)
			}
			if chunk.Message.Thinking != "" {
				accThinking.WriteString(chunk.Message.Thinking)
			}
			send("chunk", chunk)
			if chunk.Done {
				final = chunk
			}
			return nil
		})
	}

	if err != nil {
		if ctx.Err() != nil {
			s.recordCancelUsage(body.Model, accContent.String()+"\n"+accThinking.String(), startedAt)
			return
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "mlx runner failed") || strings.Contains(errMsg, "failed to initialize MLX") || strings.Contains(errMsg, "failed to load MLX") {
			if s.cfg.Language == "es" {
				errMsg = "El modelo de generación de imágenes no está soportado en este sistema operativo (Windows/Linux). Los modelos basados en MLX solo funcionan de forma nativa en dispositivos Apple Silicon (macOS)."
			} else {
				errMsg = "This image generation model is not supported on this operating system (Windows/Linux). MLX-based models only run natively on Apple Silicon (macOS) devices."
			}
		} else if isComputeOrOOMError(err) {
			if s.cfg.Language == "es" {
				errMsg = "El modelo se quedó sin memoria suficiente (VRAM / RAM) para procesar esta solicitud. Se intentó liberar memoria descargando procesos de modelos en segundo plano, pero la GPU/sistema no pudo procesar el contexto. Prueba reduciendo el tamaño del contexto (num_ctx), cerrando aplicaciones pesadas o usando una cuantización menor."
			} else {
				errMsg = "The model ran out of memory (VRAM / RAM) to process this request. An automatic attempt was made to free memory by unloading running models, but the GPU/system could not allocate sufficient memory. Try reducing the context length (num_ctx), closing heavy applications, or using a smaller quantization."
			}
		}
		send("error", map[string]any{"error": errMsg})
		return
	}

	evalCount := final.EvalCount
	evalDuration := final.EvalDuration
	promptEvalCount := final.PromptEvalCount
	if evalCount <= 0 {
		if est := estimateTextTokens(accContent.String() + "\n" + accThinking.String()); est > 0 {
			evalCount = est
		}
	}
	if promptEvalCount <= 0 {
		if est := estimatePromptTokens(body); est > 0 {
			promptEvalCount = est
		}
	}
	if evalCount > 0 && evalDuration <= 0 {
		evalDuration = int64(time.Since(startedAt))
	}
	totalTokens := promptEvalCount + evalCount
	s.recordModelUsage(body.Model, evalCount, evalDuration, promptEvalCount, time.Now())
	send("done", map[string]any{
		"elapsed_ms":         time.Since(startedAt).Milliseconds(),
		"prompt_tokens":      promptEvalCount,
		"completion_tokens":  evalCount,
		"total_tokens":       totalTokens,
		"prompt_duration_ns": final.PromptEvalDuration,
		"eval_duration_ns":   evalDuration,
		"total_duration_ns":  final.TotalDuration,
		"done_reason":        final.DoneReason,
	})
}
