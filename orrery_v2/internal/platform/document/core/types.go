package core

import "io"

type Source struct {
	Name        string
	ContentType string
	Reader      io.Reader
}

type ExtractedDocument struct {
	Title    string
	Text     string
	Sections []Section
	Metadata map[string]string
	Checksum string
}

func (d ExtractedDocument) Clone() ExtractedDocument {
	clone := d
	clone.Sections = cloneSections(d.Sections)
	clone.Metadata = cloneStringMap(d.Metadata)
	return clone
}

type Section struct {
	HeadingPath []string
	Text        string
	StartOffset int
	EndOffset   int
}

func (s Section) Clone() Section {
	clone := s
	clone.HeadingPath = cloneStrings(s.HeadingPath)
	return clone
}

type ChunkPolicy struct {
	MaxRunes      int  `json:"max_runes"`
	OverlapRunes  int  `json:"overlap_runes"`
	MinRunes      int  `json:"min_runes"`
	PreserveHeads bool `json:"preserve_heads"`
}

type Chunk struct {
	Ordinal     int
	Text        string
	HeadingPath []string
	StartOffset int
	EndOffset   int
	ContentHash string
	Metadata    map[string]string
}

func (c Chunk) Clone() Chunk {
	clone := c
	clone.HeadingPath = cloneStrings(c.HeadingPath)
	clone.Metadata = cloneStringMap(c.Metadata)
	return clone
}

func cloneSections(in []Section) []Section {
	if len(in) == 0 {
		return nil
	}
	out := make([]Section, len(in))
	for i, section := range in {
		out[i] = section.Clone()
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
