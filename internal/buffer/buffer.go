package buffer

import (
	"regexp"
	"strings"
	"time"
)

// SentenceBuffer accumulates text chunks and detects sentence boundaries.
type SentenceBuffer struct {
	buffer             string
	partialTimeoutSec  float64
	maxSentenceChars   int
	lastUpdateTime     time.Time
	maxBufferSize      int
	sentenceEndRegex   *regexp.Regexp
	secondarySplitRegex *regexp.Regexp
	abbreviations      map[string]bool
}

// NewSentenceBuffer creates a new SentenceBuffer.
func NewSentenceBuffer(partialTimeoutSec float64, maxSentenceChars int) *SentenceBuffer {
	return &SentenceBuffer{
		buffer:            "",
		partialTimeoutSec: partialTimeoutSec,
		maxSentenceChars:  maxSentenceChars,
		lastUpdateTime:    time.Now(),
		maxBufferSize:     10000,
		sentenceEndRegex:  regexp.MustCompile(`([.!?]+)(\s+|$)`),
		secondarySplitRegex: regexp.MustCompile(`([,;:\-]+)\s+`),
		abbreviations: map[string]bool{
			"mr":   true,
			"md":   true,
			"dr":   true,
			"mrs":  true,
			"ms":   true,
			"prof": true,
			"st":   true,
			"ave":  true,
			"rd":   true,
			"etc":  true,
			"vs":   true,
		},
	}
}

// IsOverflow checks if adding chunk would cause buffer overflow.
func (sb *SentenceBuffer) IsOverflow(incomingChunk string) bool {
	return len(sb.buffer)+len(incomingChunk) > sb.maxBufferSize
}

// AddChunk adds a text chunk and return any completed sentences.
func (sb *SentenceBuffer) AddChunk(chunk string) []string {
	if chunk == "" {
		return nil
	}

	sb.buffer += chunk
	sb.lastUpdateTime = time.Now()

	var sentences []string

	for {
		indices := sb.findSentenceEnd(sb.buffer)
		if indices == nil {
			// No sentence boundary found - check for long sentence split
			if len(sb.buffer) > sb.maxSentenceChars {
				splitSentence := sb.splitLongSentence()
				if splitSentence != "" {
					sentences = append(sentences, splitSentence)
					continue
				}
			}
			break
		}

		endIdx := indices[1]
		sentence := strings.TrimSpace(sb.buffer[:endIdx])

		// If sentence is too long, split it
		if len(sentence) > sb.maxSentenceChars {
			splitSentences := sb.splitSentenceAtBoundary(sentence)
			sentences = append(sentences, splitSentences...)
		} else {
			sentences = append(sentences, sentence)
		}

		sb.buffer = sb.buffer[endIdx:]
	}

	return sentences
}

// findSentenceEnd finds the end of a sentence, avoiding abbreviations.
func (sb *SentenceBuffer) findSentenceEnd(text string) []int {
	matches := sb.sentenceEndRegex.FindAllStringSubmatchIndex(text, -1)
	for _, m := range matches {
		punIdx := m[2] // Index of the punctuation
		// Check the word before the punctuation
		before := text[:punIdx]
		words := strings.Fields(before)
		if len(words) > 0 {
			lastWord := strings.ToLower(strings.Trim(words[len(words)-1], " .!?;:"))
			if sb.abbreviations[lastWord] {
				continue // Skip this match, it's an abbreviation
			}
		}
		return []int{m[0], m[1]}
	}
	return nil
}

func (sb *SentenceBuffer) splitLongSentence() string {
	// Try secondary punctuation first
	match := sb.secondarySplitRegex.FindStringIndex(sb.buffer)
	if match != nil && match[1] <= sb.maxSentenceChars {
		splitIdx := match[1]
		sentence := strings.TrimSpace(sb.buffer[:splitIdx])
		sb.buffer = sb.buffer[splitIdx:]
		return sentence
	}

	// Fallback: split at last word boundary before max length
	if len(sb.buffer) > sb.maxSentenceChars {
		searchRegion := sb.buffer[:sb.maxSentenceChars]
		lastSpace := strings.LastIndex(searchRegion, " ")
		if lastSpace > 20 {
			sentence := strings.TrimSpace(sb.buffer[:lastSpace])
			sb.buffer = strings.TrimLeft(sb.buffer[lastSpace:], " ")
			return sentence
		}

		// Last resort: force split
		sentence := strings.TrimSpace(sb.buffer[:sb.maxSentenceChars])
		sb.buffer = sb.buffer[sb.maxSentenceChars:]
		return sentence
	}
	return ""
}

func (sb *SentenceBuffer) splitSentenceAtBoundary(sentence string) []string {
	var chunks []string
	remaining := sentence

	for len(remaining) > sb.maxSentenceChars {
		match := sb.secondarySplitRegex.FindStringIndex(remaining)
		if match != nil && match[1] <= sb.maxSentenceChars {
			splitIdx := match[1]
			chunks = append(chunks, strings.TrimSpace(remaining[:splitIdx]))
			remaining = remaining[splitIdx:]
			continue
		}

		searchRegion := remaining[:sb.maxSentenceChars]
		lastSpace := strings.LastIndex(searchRegion, " ")
		if lastSpace > 20 {
			chunks = append(chunks, strings.TrimSpace(remaining[:lastSpace]))
			remaining = strings.TrimLeft(remaining[lastSpace:], " ")
			continue
		}

		chunks = append(chunks, strings.TrimSpace(remaining[:sb.maxSentenceChars]))
		remaining = remaining[sb.maxSentenceChars:]
	}

	if strings.TrimSpace(remaining) != "" {
		chunks = append(chunks, strings.TrimSpace(remaining))
	}

	return chunks
}

// Flush force flushes the current buffer as a sentence.
func (sb *SentenceBuffer) Flush() string {
	sentence := strings.TrimSpace(sb.buffer)
	sb.buffer = ""
	return sentence
}

// CheckTimeout returns buffer content if it has timed out.
func (sb *SentenceBuffer) CheckTimeout() string {
	if strings.TrimSpace(sb.buffer) != "" && time.Since(sb.lastUpdateTime).Seconds() > sb.partialTimeoutSec {
		return sb.Flush()
	}
	return ""
}
