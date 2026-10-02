package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv reads a .env file and sets environment variables if they are not already set.
// If filename is empty, it defaults to ".env".
func LoadDotEnv(filenames ...string) error {
	targetFiles := filenames
	if len(targetFiles) == 0 {
		targetFiles = []string{".env"}
	}

	for _, filename := range targetFiles {
		file, err := os.Open(filename)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			// Support "export KEY=VAL" format
			if strings.HasPrefix(line, "export ") {
				line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
			}

			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}

			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])

			// Strip surrounding single or double quotes
			if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
				(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
				if len(val) >= 2 {
					val = val[1 : len(val)-1]
				}
			}

			// Only set if not already defined in process environment
			if _, exists := os.LookupEnv(key); !exists && key != "" {
				_ = os.Setenv(key, val)
			}
		}

		if err := scanner.Err(); err != nil {
			return err
		}
	}

	return nil
}
