package probe

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

var cryptoReader io.Reader = cryptorand.Reader

type generatedTask struct {
	Prompt          string
	RetainedWord    string
	TemplateVersion string
}

var taskTemplates = []string{
	"请按相反顺序写出“%s、%s、%s”，并在末尾原样附上保留词“%s”。",
	"请将“%s靠近%s时看见%s”改写得更简洁，并在末尾原样附上保留词“%s”。",
	"请用不超过十二个字描述“%s映着%s和%s”，并在末尾原样附上保留词“%s”。",
}

var taskWords = []string{
	"松针", "白瓷", "微雨", "石阶", "纸舟", "晨雾", "木窗", "月影", "溪流", "风铃",
}

var retainedAdjectives = []string{"清澈", "安静", "轻盈", "明亮", "温和", "素净"}

var retainedNouns = []string{"松塔", "纸舟", "石阶", "风铃", "月影", "木窗"}

func generateTask(reader io.Reader) (generatedTask, error) {
	if reader == nil {
		return generatedTask{}, errors.New("random source is required")
	}
	templateIndex, err := randomIndex(reader, len(taskTemplates))
	if err != nil {
		return generatedTask{}, err
	}
	availableWords := append([]string(nil), taskWords...)
	selectedWords := make([]string, 3)
	for index := range selectedWords {
		wordIndex, indexErr := randomIndex(reader, len(availableWords))
		if indexErr != nil {
			return generatedTask{}, indexErr
		}
		selectedWords[index] = availableWords[wordIndex]
		availableWords = append(availableWords[:wordIndex], availableWords[wordIndex+1:]...)
	}
	adjectiveIndex, err := randomIndex(reader, len(retainedAdjectives))
	if err != nil {
		return generatedTask{}, err
	}
	nounIndex, err := randomIndex(reader, len(retainedNouns))
	if err != nil {
		return generatedTask{}, err
	}
	numberIndex, err := randomIndex(reader, 90)
	if err != nil {
		return generatedTask{}, err
	}
	retainedWord := fmt.Sprintf("%s%s%02d", retainedAdjectives[adjectiveIndex], retainedNouns[nounIndex], numberIndex+10)
	prompt := fmt.Sprintf(taskTemplates[templateIndex], selectedWords[0], selectedWords[1], selectedWords[2], retainedWord)
	if containsForbiddenPrompt(prompt) {
		return generatedTask{}, errors.New("generated task failed safety validation")
	}
	return generatedTask{Prompt: prompt, RetainedWord: retainedWord, TemplateVersion: TemplateVersion}, nil
}

func randomIndex(reader io.Reader, size int) (int, error) {
	if size <= 0 {
		return 0, errors.New("random choice is empty")
	}
	var value [8]byte
	if _, err := io.ReadFull(reader, value[:]); err != nil {
		return 0, errors.New("secure random generation failed")
	}
	return int(binary.BigEndian.Uint64(value[:]) % uint64(size)), nil
}

func containsForbiddenPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(prompt, " ", ""))
	for _, forbidden := range []string{"hi", "hello", "你是谁", "回答ok"} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return false
}
