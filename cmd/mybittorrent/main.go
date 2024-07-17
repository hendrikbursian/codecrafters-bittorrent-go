package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
	// bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

func decodeBencode(bencodedString string) (interface{}, error) {
	if unicode.IsDigit(rune(bencodedString[0])) {
		var firstColonIndex int

		for i := 0; i < len(bencodedString); i++ {
			if bencodedString[i] == ':' {
				firstColonIndex = i
				break
			}
		}

		lengthStr := bencodedString[:firstColonIndex]

		length, err := strconv.Atoi(lengthStr)
		if err != nil {
			return "", err
		}

		return bencodedString[firstColonIndex+1 : firstColonIndex+1+length], nil
	}
	r, size := utf8.DecodeRuneInString(bencodedString)
	if r == 'i' {
		eIdx := strings.IndexRune(bencodedString, 'e')
		if eIdx == -1 {
			return "", fmt.Errorf("Integer not ended by 'e'")
		}
		int, err := strconv.Atoi(bencodedString[size:eIdx])
		if err != nil {
			return "", fmt.Errorf("Cannot decode integer: %+v", err)
		}
		return int, nil
	}

	return "", fmt.Errorf("Only strings are supported at the moment")
}

func main() {
	command := os.Args[1]

	if command == "decode" {
		bencodedValue := os.Args[2]

		decoded, err := decodeBencode(bencodedValue)
		if err != nil {
			fmt.Println(err)
			return
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
