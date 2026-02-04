import re
import time
from typing import List, Optional


class SentenceBuffer:
    """
    Accumulates text chunks and detects sentence boundaries.
    Handles abbreviations, timeouts, and long sentence splitting.
    """

    # Sentence-ending punctuation followed by space or end of string
    # Uses negative lookbehind for common abbreviations
    ABBREVIATION_PATTERN = r'(?<![MD]r)(?<!Mrs)(?<!Ms)(?<!Prof)(?<!St)(?<!Ave)(?<!Rd)(?<!etc)(?<!vs)'
    SENTENCE_END_REGEX = re.compile(
        ABBREVIATION_PATTERN + r'([.!?]+)(?:\s+|$)',
        re.IGNORECASE
    )

    # Secondary split points for long sentences
    SECONDARY_SPLIT_REGEX = re.compile(r'([,;:\-]+)\s+')

    # Max characters before forcing a split (~150 chars = ~10s at normal speed)
    MAX_SENTENCE_CHARS = 150

    # Max buffer size for overflow protection (DoS prevention)
    MAX_BUFFER_SIZE = 10000

    def __init__(self, partial_timeout_sec: float = 2.0, max_sentence_chars: int = 150):
        self.buffer = ""
        self.partial_timeout_sec = partial_timeout_sec
        self.max_sentence_chars = max_sentence_chars
        self.last_update_time = time.time()

    def is_overflow(self, incoming_chunk: str) -> bool:
        """Check if adding chunk would cause buffer overflow."""
        return len(self.buffer) + len(incoming_chunk) > self.MAX_BUFFER_SIZE

    def add_chunk(self, chunk: str) -> List[str]:
        """Add a text chunk and return any completed sentences."""
        if not chunk:
            return []

        self.buffer += chunk
        self.last_update_time = time.time()

        sentences = []

        while True:
            match = self.SENTENCE_END_REGEX.search(self.buffer)
            if not match:
                # No sentence boundary found - check for long sentence split
                if len(self.buffer) > self.max_sentence_chars:
                    split_sentence = self._split_long_sentence()
                    if split_sentence:
                        sentences.append(split_sentence)
                        continue
                break

            end_idx = match.end()
            sentence = self.buffer[:end_idx].strip()

            # If sentence is too long, split it
            if len(sentence) > self.max_sentence_chars:
                split_sentences = self._split_sentence_at_boundary(sentence)
                sentences.extend(split_sentences)
            else:
                sentences.append(sentence)

            self.buffer = self.buffer[end_idx:]

        return sentences

    def _split_long_sentence(self) -> Optional[str]:
        """Split buffer at secondary punctuation or word boundary."""
        # Try secondary punctuation first (comma, semicolon, etc.)
        match = self.SECONDARY_SPLIT_REGEX.search(self.buffer[:self.max_sentence_chars])
        if match:
            # Split after the punctuation and space
            split_idx = match.end()
            sentence = self.buffer[:split_idx].strip()
            self.buffer = self.buffer[split_idx:]
            return sentence

        # Fallback: split at last word boundary before max length
        search_region = self.buffer[:self.max_sentence_chars]
        last_space = search_region.rfind(' ')
        if last_space > 20:  # Ensure we have a reasonable chunk
            sentence = self.buffer[:last_space].strip()
            self.buffer = self.buffer[last_space:].lstrip()
            return sentence

        # Last resort: force split at max length
        sentence = self.buffer[:self.max_sentence_chars].strip()
        self.buffer = self.buffer[self.max_sentence_chars:]
        return sentence

    def _split_sentence_at_boundary(self, sentence: str) -> List[str]:
        """Split a completed sentence that's too long into smaller chunks."""
        chunks = []
        remaining = sentence

        while len(remaining) > self.max_sentence_chars:
            # Try secondary punctuation
            match = self.SECONDARY_SPLIT_REGEX.search(remaining[:self.max_sentence_chars])
            if match:
                split_idx = match.end()
                chunks.append(remaining[:split_idx].strip())
                remaining = remaining[split_idx:]
                continue

            # Fallback to word boundary
            search_region = remaining[:self.max_sentence_chars]
            last_space = search_region.rfind(' ')
            if last_space > 20:
                chunks.append(remaining[:last_space].strip())
                remaining = remaining[last_space:].lstrip()
                continue

            # Force split
            chunks.append(remaining[:self.max_sentence_chars].strip())
            remaining = remaining[self.max_sentence_chars:]

        if remaining.strip():
            chunks.append(remaining.strip())

        return chunks

    def flush(self) -> Optional[str]:
        """Force flush the current buffer as a sentence."""
        if not self.buffer.strip():
            return None
        sentence = self.buffer.strip()
        self.buffer = ""
        return sentence

    def check_timeout(self) -> Optional[str]:
        """Return buffer content if it has timed out."""
        if self.buffer.strip() and (time.time() - self.last_update_time > self.partial_timeout_sec):
            return self.flush()
        return None
