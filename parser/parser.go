package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type InstructionType string

const (
	InstructionFrom    InstructionType = "FROM"
	InstructionCopy    InstructionType = "COPY"
	InstructionRun     InstructionType = "RUN"
	InstructionWorkdir InstructionType = "WORKDIR"
	InstructionEnv     InstructionType = "ENV"
	InstructionCmd     InstructionType = "CMD"
)

type Instruction struct {
	Type   InstructionType `json:"type"`
	Args   []string        `json:"args,omitempty"`
	Raw    string          `json:"raw"`
	Line   int             `json:"line"`
	Value  string          `json:"value,omitempty"`
	Key    string          `json:"key,omitempty"`
	JSON   []string        `json:"json,omitempty"`
}

func ParseFile(path string) ([]Instruction, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Docksmithfile not found at %s", path)
		}
		return nil, fmt.Errorf("read Docksmithfile: %w", err)
	}
	instructions, err := Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return instructions, nil
}

func Parse(contents string) ([]Instruction, error) {
	lines := strings.Split(contents, "\n")
	instructions := make([]Instruction, 0, len(lines))

	for i, line := range lines {
		lineNumber := i + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		fields := strings.Fields(trimmed)
		opcode := fields[0]
		remainder := ""
		if len(fields) == 1 {
			opcode = trimmed
		} else {
			remainder = strings.TrimSpace(trimmed[len(opcode):])
		}

		switch strings.ToUpper(opcode) {
		case string(InstructionFrom):
			instruction, err := parseFrom(lineNumber, trimmed, remainder)
			if err != nil {
				return nil, err
			}
			instructions = append(instructions, instruction)
		case string(InstructionCopy):
			instruction, err := parseCopy(lineNumber, trimmed, remainder)
			if err != nil {
				return nil, err
			}
			instructions = append(instructions, instruction)
		case string(InstructionRun):
			instruction, err := parseRun(lineNumber, trimmed, remainder)
			if err != nil {
				return nil, err
			}
			instructions = append(instructions, instruction)
		case string(InstructionWorkdir):
			instruction, err := parseWorkdir(lineNumber, trimmed, remainder)
			if err != nil {
				return nil, err
			}
			instructions = append(instructions, instruction)
		case string(InstructionEnv):
			instruction, err := parseEnv(lineNumber, trimmed, remainder)
			if err != nil {
				return nil, err
			}
			instructions = append(instructions, instruction)
		case string(InstructionCmd):
			instruction, err := parseCmd(lineNumber, trimmed, remainder)
			if err != nil {
				return nil, err
			}
			instructions = append(instructions, instruction)
		default:
			return nil, lineError(lineNumber, "unknown instruction %q", opcode)
		}
	}

	if len(instructions) == 0 {
		return nil, fmt.Errorf("no instructions found")
	}
	if instructions[0].Type != InstructionFrom {
		return nil, lineError(instructions[0].Line, "first instruction must be FROM")
	}
	return instructions, nil
}

func parseFrom(line int, raw string, remainder string) (Instruction, error) {
	if remainder == "" {
		return Instruction{}, lineError(line, "FROM requires an image reference")
	}
	return Instruction{
		Type:  InstructionFrom,
		Args:  []string{remainder},
		Raw:   raw,
		Line:  line,
		Value: remainder,
	}, nil
}

func parseCopy(line int, raw string, remainder string) (Instruction, error) {
	fields := strings.Fields(remainder)
	if len(fields) != 2 {
		return Instruction{}, lineError(line, "COPY requires exactly 2 arguments: <src> <dest>")
	}
	return Instruction{
		Type: InstructionCopy,
		Args: fields,
		Raw:  raw,
		Line: line,
	}, nil
}

func parseRun(line int, raw string, remainder string) (Instruction, error) {
	if remainder == "" {
		return Instruction{}, lineError(line, "RUN requires a command")
	}
	return Instruction{
		Type:  InstructionRun,
		Args:  []string{remainder},
		Raw:   raw,
		Line:  line,
		Value: remainder,
	}, nil
}

func parseWorkdir(line int, raw string, remainder string) (Instruction, error) {
	if remainder == "" {
		return Instruction{}, lineError(line, "WORKDIR requires a path")
	}
	return Instruction{
		Type:  InstructionWorkdir,
		Args:  []string{remainder},
		Raw:   raw,
		Line:  line,
		Value: remainder,
	}, nil
}

func parseEnv(line int, raw string, remainder string) (Instruction, error) {
	key, value, ok := strings.Cut(remainder, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return Instruction{}, lineError(line, "ENV requires KEY=VALUE form")
	}
	return Instruction{
		Type:  InstructionEnv,
		Raw:   raw,
		Line:  line,
		Key:   key,
		Value: value,
		Args:  []string{key + "=" + value},
	}, nil
}

func parseCmd(line int, raw string, remainder string) (Instruction, error) {
	if remainder == "" {
		return Instruction{}, lineError(line, "CMD requires a JSON array")
	}
	var parts []string
	if err := json.Unmarshal([]byte(remainder), &parts); err != nil {
		return Instruction{}, lineError(line, "CMD must be a valid JSON array of strings: %v", err)
	}
	if len(parts) == 0 {
		return Instruction{}, lineError(line, "CMD JSON array cannot be empty")
	}
	return Instruction{
		Type: InstructionCmd,
		Raw:  raw,
		Line: line,
		JSON: parts,
	}, nil
}

func lineError(line int, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, args...))
}
