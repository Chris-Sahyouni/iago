package exe

import (
	"encoding/binary"
	"encoding/hex"
	"errors"

	"github.com/Chris-Sahyouni/iago/isa"
	"github.com/Chris-Sahyouni/iago/term"
	"github.com/Chris-Sahyouni/iago/trie"
)

type Elf struct {
	arch                     uint   // either 32 or 64
	endianness               string // either "big" or "little"
	isa                      isa.ISA
	contents                 []byte
	programHeaderTableOffset uint
	reverseInstructionTrie   *trie.TrieNode
}

type elfField struct {
	offset32 uint
	offset64 uint
	size32   uint
	size64   uint
}

type segment struct {
	VAddr  uint
	Offset uint
	Size   uint
}

var elfHeader = map[string]elfField{
	"arch":                             {0x04, 0x04, 1, 1},
	"endianness":                       {0x05, 0x05, 1, 1},
	"isa":                              {0x12, 0x12, 1, 1}, // technically this field is 2 bytes, but the 2nd byte is only used for two obscure ISAs
	"entry point":                      {0x18, 0x18, 4, 8},
	"program header table offset":      {0x1c, 0x20, 4, 8},
	"program header table entry size":  {0x2a, 0x36, 2, 2},
	"program header table num entries": {0x2c, 0x38, 2, 2},
}

var programHeaderEntry = map[string]elfField{
	"segment type":    {0x00, 0x00, 4, 4},
	"flags":           {0x18, 0x04, 4, 4},
	"segment offset":  {0x04, 0x08, 4, 8},
	"virtual address": {0x08, 0x10, 4, 8},
	"file size":       {0x10, 0x20, 4, 8},
	"mem size":        {0x14, 0x28, 4, 8},
}

func (e *Elf) Info() {
	term.Println("  File Type: ELF")
	term.Println("  Arch:", e.arch)
	term.Println("  ISA:", e.isa.Name())
	term.Println("  Endianness:", e.endianness)
}

func NewElf(elfContents []byte, args map[string]string) (Executable, error) {

	// to detect thumb mode in ARM binaries
	_, thumb := args["---thumb"]

	arch, err := elfArch(elfContents)
	if err != nil {
		return nil, err
	}

	endianness, err := elfEndianness(elfContents)
	if err != nil {
		return nil, err
	}

	elf := &Elf{
		arch:                     arch,
		endianness:               endianness,
		contents:                 elfContents,
		programHeaderTableOffset: 0,
		isa:                      nil,
	}

	err = elf.setISA(thumb)
	if err != nil {
		return nil, err
	}

	executableSegments, err := elf.locateExecutableSegments()
	if err != nil {
		return nil, err
	}

	instructionStream := elf.InstructionStream(executableSegments)

	if len(instructionStream) == 0 {
		return nil, errors.New("no executable segments found")
	}

	elf.reverseInstructionTrie = trie.Trie(instructionStream, elf.isa)

	return elf, nil
}

func elfArch(elfContents []byte) (uint, error) {
	archOffset := 0x04

	if len(elfContents) < archOffset {
		return 0, errors.New("invalid ELF file: file size less than offset to arch field")
	}

	if elfContents[archOffset] == 1 {
		return 32, nil
	} else if elfContents[archOffset] == 2 {
		return 64, nil
	} else {
		return 0, errors.New("invalid ELF file: arch field not 32 or 64")
	}
}

func elfEndianness(elfContents []byte) (string, error) {
	endiannessOffset := 0x05

	if len(elfContents) < endiannessOffset {
		return "", errors.New("invalid ELF file: file size less than offset to endianness field")
	}

	if elfContents[endiannessOffset] == 1 {
		return "little", nil
	} else if elfContents[endiannessOffset] == 2 {
		return "big", nil
	} else {
		return "", errors.New("invalid ELF file: endianness field not \"little\" or \"big\"")
	}
}

func (e *Elf) fieldValue(field string, targetHeader map[string]elfField, baseOffset uint) (uint, error) {
	var offset uint
	var size uint

	fieldInfo := targetHeader[field]
	if e.arch == 32 {
		offset = fieldInfo.offset32
		size = fieldInfo.size32
	} else if e.arch == 64 {
		offset = fieldInfo.offset64
		size = fieldInfo.size64
	}

	offset += baseOffset

	if len(e.contents) < int(offset+size) {
		return 0, errors.New("invalid ELF file: value offset outside file bounds")
	}

	value := e.contents[offset : offset+size]

	if size == 1 {
		return uint(value[0]), nil
	}

	var byteOrder binary.ByteOrder

	if e.endianness == "big" {
		byteOrder = binary.BigEndian
	} else if e.endianness == "little" {
		byteOrder = binary.LittleEndian
	}

	if size == 2 {
		return uint(byteOrder.Uint16(value)), nil
	} else if size == 4 {
		return uint(byteOrder.Uint32(value)), nil
	} else if size == 8 {
		return uint(byteOrder.Uint64(value)), nil
	}

	return 0, nil // never reached
}

func (e *Elf) setISA(thumb bool) error {

	// maps the value present in the elf file to an ISA
	var supportedISAs = map[uint]isa.ISA{
		0x03: isa.X86{},
		0x3e: isa.X86{},
		0x28: isa.ARM{}, // requires check for thumb mode
		0xb7: isa.AArch64{},
	}

	elfIsaValue, err := e.fieldValue("isa", elfHeader, 0)
	if err != nil {
		return err
	}

	elfIsa, ok := supportedISAs[elfIsaValue]
	if ok {

		if elfIsaValue == 0x28 { // arm
			if thumb {
				elfIsa = isa.Thumb{}
			} else {
				term.Println("ARM binary detected. To target thumb mode re-load using the --thumb flag")
			}
		} else {
			if thumb {
				term.Println("--thumb flag ignored: --thumb flag should only be used on ARM binaries")
			}
		}

		e.isa = elfIsa
		return nil
	}

	// isa not supported
	return errors.ErrUnsupported
}

func (e *Elf) locateExecutableSegments() ([]segment, error) {
	var segments []segment

	programHeaderTableOffset, err := e.fieldValue("program header table offset", elfHeader, 0)
	if err != nil {
		return nil, err
	}
	programHeaderTableEntrySize, err := e.fieldValue("program header table entry size", elfHeader, 0)
	if err != nil {
		return nil, err
	}
	numEntries, err := e.fieldValue("program header table num entries", elfHeader, 0)
	if err != nil {
		return nil, err
	}

	if numEntries == 0 {
		return nil, errors.New("invalid ELF file: empty program header table")
	}

	for i := range numEntries {
		entryOffset := programHeaderTableOffset + (i * programHeaderTableEntrySize)
		flags, err := e.fieldValue("flags", programHeaderEntry, entryOffset)
		if err != nil {
			return nil, err
		}

		var executableFlagMask uint = 0x1
		if flags&executableFlagMask > 0 {
			segmentOffset, err := e.fieldValue("segment offset", programHeaderEntry, entryOffset)
			if err != nil {
				return nil, err
			}
			virtualAddress, err := e.fieldValue("virtual address", programHeaderEntry, entryOffset)
			if err != nil {
				return nil, err
			}
			sizeInFile, err := e.fieldValue("file size", programHeaderEntry, entryOffset)
			if err != nil {
				return nil, err
			}

			segments = append(segments, segment{
				VAddr:  virtualAddress,
				Offset: segmentOffset,
				Size:   sizeInFile,
			})

		}
	}
	return segments, nil
}

func (e *Elf) InstructionStream(executableSegments []segment) []isa.Instruction {
	var instructionStream []isa.Instruction
	instructionSize := e.isa.InstructionSize()
	for _, segment := range executableSegments {
		segmentContents := e.contents[segment.Offset : segment.Offset+segment.Size]
		for i := 0; i < len(segmentContents); i += instructionSize {
			newInstruction := isa.Instruction{
				// make sure this is correct for big endian programs too
				Op:    hex.EncodeToString(segmentContents[i : i+instructionSize]),
				Vaddr: segment.VAddr + uint(i),
			}
			instructionStream = append(instructionStream, newInstruction)
		}
	}
	return instructionStream
}

func (e *Elf) ReverseInstructionTrie() *trie.TrieNode {
	return e.reverseInstructionTrie
}

func (e *Elf) Endianness() string {
	return e.endianness
}

func (e *Elf) Arch() uint {
	return e.arch
}

func (e *Elf) Isa() isa.ISA {
	return e.isa
}
