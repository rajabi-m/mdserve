package server

import (
	"os"
	"strings"
)

// flattenMtimes shares the file access policy with requests and static inputs.
func (s *Server) flattenMtimes() map[string]float64 {
	out := map[string]float64{}
	_ = s.walkFiles(".", func(rel string, info os.FileInfo) error {
		if strings.HasSuffix(strings.ToLower(rel), ".md") {
			out[rel] = float64(info.ModTime().UnixNano()) / 1e9
		}
		return nil
	})
	return out
}
