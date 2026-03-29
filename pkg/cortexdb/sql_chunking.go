package cortexdb

const sqliteMaxVariables = 999

func sqlChunkSize(varsPerValue int) int {
	if varsPerValue <= 0 {
		varsPerValue = 1
	}
	size := sqliteMaxVariables / varsPerValue
	if size <= 0 {
		return 1
	}
	return size
}

func stringChunks(values []string, varsPerValue int) [][]string {
	if len(values) == 0 {
		return nil
	}
	size := sqlChunkSize(varsPerValue)
	chunks := make([][]string, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		end := min(start+size, len(values))
		chunks = append(chunks, values[start:end])
	}
	return chunks
}
