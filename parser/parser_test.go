package parser

import "testing"

func TestParseValidDocksmithfile(t *testing.T) {
	content := `FROM base:latest
WORKDIR /app
ENV MODE=demo
COPY . /app
RUN echo hello
CMD ["python","main.py"]`

	instructions, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if got, want := len(instructions), 6; got != want {
		t.Fatalf("len(instructions) = %d, want %d", got, want)
	}

	if instructions[0].Type != InstructionFrom {
		t.Fatalf("first instruction type = %s, want %s", instructions[0].Type, InstructionFrom)
	}
	if instructions[5].Type != InstructionCmd {
		t.Fatalf("last instruction type = %s, want %s", instructions[5].Type, InstructionCmd)
	}
}

func TestParseRejectsUnknownInstruction(t *testing.T) {
	_, err := Parse("FROM base:latest\nEXPOSE 8080")
	if err == nil {
		t.Fatal("expected error for unknown instruction")
	}
}

func TestParseRejectsInvalidCmd(t *testing.T) {
	_, err := Parse("FROM base:latest\nCMD python main.py")
	if err == nil {
		t.Fatal("expected error for invalid CMD")
	}
}
