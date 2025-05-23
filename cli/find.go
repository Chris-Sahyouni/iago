package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Chris-Sahyouni/iago/global"
	"github.com/Chris-Sahyouni/iago/term"
)

type Find struct{ args Args }

func (f Find) ValidArgs() bool {
	if len(f.args) != 1 {
		return false
	}

	_, ok := f.args["default"]
	return ok
}

func (f Find) Execute(globalState *global.GlobalState) error {
	target, _ := f.args["default"]
	currFile := globalState.CurrentFile

	if currFile == nil {
		return errors.New("no target file specified")
	}

	vaddr, err := currFile.ReverseInstructionTrie().Find(target, currFile.Isa())
	if err != nil {
		return err
	}
	fmtString := fmt.Sprintf("virtual address: %x", vaddr)
	term.Println(fmtString)
	return nil
}

func (Find) Help() {
	term.Println("    find <gadget>" + strings.Repeat(" ", SPACE_BETWEEN-len("find <gadget>")) + "Searches the current binary for <gadget> and returns its virtual address if found,")
	term.Println(strings.Repeat(" ", SPACE_BETWEEN+4) + "<gadget> should be inputted as the hex representation of the machine code of <gadget>")
}
