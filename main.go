package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const INF = math.MaxFloat64

// Edge описывает одно направление с весом и биржей.
type Edge struct {
	To       string  `json:"to"`
	Weight   float64 // -log(rate)
	Exchange string
}

// Graph хранит вершины (currencies), ребра и для каждого from->to список возможных Edge.
type Graph struct {
	vertices []string
	edges    map[string][]Edge
}

func NewGraph() *Graph {
	return &Graph{
		vertices: []string{},
		edges:    make(map[string][]Edge),
	}
}

// AddEdge регистрирует новую вершину и добавляет ребро.
func (g *Graph) AddEdge(from, to string, rate float64, exchange string) {
	if _, ok := g.edges[from]; !ok {
		g.vertices = append(g.vertices, from)
	}
	if _, ok := g.edges[to]; !ok {
		g.vertices = append(g.vertices, to)
	}
	g.edges[from] = append(g.edges[from], Edge{
		To:       to,
		Weight:   -math.Log(rate),
		Exchange: exchange,
	})
}

// cycleInfo хранит один найденный цикл и его характеристики.
type cycleInfo struct {
	Path      []string
	CycleType string
	Profit    float64
}

// detectArbitrage запускает параллельно поиск циклов для каждой валюты.
func (g *Graph) detectArbitrage() {
	var wg sync.WaitGroup
	var cycles []cycleInfo
	var cyclesMtx sync.Mutex
	uniqueCycles := make(map[string]bool)

	// Ограничиваем длину цикла числом переходов (ребер).
	maxDepth := 5

	// Для каждой валюты запускаем поиск циклов.
	for _, start := range g.vertices {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			visited := make(map[string]bool)
			visited[s] = true
			g.dfs(s, s, []string{s}, []string{}, 0, 0.0, visited, maxDepth, &cycles, &cyclesMtx, uniqueCycles)
		}(start)
	}
	wg.Wait()

	// Сортировка циклов по убыванию % прибыли.
	sort.Slice(cycles, func(i, j int) bool {
		return cycles[i].Profit > cycles[j].Profit
	})

	limit := 10
	if len(cycles) < limit {
		limit = len(cycles)
	}

	for i := 0; i < limit; i++ {
		c := cycles[i]
		// Явно выводим исходную валюту как начало и конец цикла.
		fmt.Printf("#%d: profit=%.4f%%, %s cycle (starts and ends with %s): ", i+1, c.Profit*100, c.CycleType, c.Path[0])
		for idx, v := range c.Path {
			if idx > 0 {
				fmt.Print(" -> ")
			}
			fmt.Print(v)
		}
		fmt.Printf(" -> %s\n", c.Path[0])
	}
}

// dfs ищет циклы с ограничением по глубине. Если цикл замыкается (current == start)
// при любой глубине, не превышающей maxDepth, он регистрируется.
func (g *Graph) dfs(
	start, current string,
	path, exchanges []string,
	depth int,
	accWeight float64,
	visited map[string]bool,
	maxDepth int,
	cycles *[]cycleInfo,
	cyclesMtx *sync.Mutex,
	uniqueCycles map[string]bool,
) {
	// Если мы замкнули цикл (и это не тривиальный путь), регистрируем его.
	if depth > 0 && current == start {
		if accWeight < 0 { // прибыльный цикл
			norm := normalizeCycle(path)
			cyclesMtx.Lock()
			if uniqueCycles[norm] {
				cyclesMtx.Unlock()
				return
			}
			uniqueCycles[norm] = true
			cyclesMtx.Unlock()

			// Определяем тип цикла по биржам.
			exSet := make(map[string]struct{})
			for _, ex := range exchanges {
				exSet[ex] = struct{}{}
			}
			ctype := "intra-exchange"
			if len(exSet) > 1 {
				ctype = "inter-exchange"
			}
			// Вычисляем profit как: product - 1.
			profit := math.Exp(-accWeight) - 1

			cyclesMtx.Lock()
			*cycles = append(*cycles, cycleInfo{
				Path:      append([]string(nil), path...),
				CycleType: ctype,
				Profit:    profit,
			})
			cyclesMtx.Unlock()
		}
		// Замыкание цикла – не продолжаем расширять эту ветку.
		return
	}
	if depth >= maxDepth {
		return
	}
	// Продолжаем обход по всем ребрам из текущей валюты.
	for _, e := range g.edges[current] {
		next := e.To
		// Разрешаем переход к исходной валюте даже если она уже посещена.
		if next != start && visited[next] {
			continue
		}
		newPath := append(path, next)
		newExchanges := append(exchanges, e.Exchange)
		if next != start {
			visited[next] = true
			g.dfs(start, next, newPath, newExchanges, depth+1, accWeight+e.Weight, visited, maxDepth, cycles, cyclesMtx, uniqueCycles)
			visited[next] = false
		} else {
			g.dfs(start, next, newPath, newExchanges, depth+1, accWeight+e.Weight, visited, maxDepth, cycles, cyclesMtx, uniqueCycles)
		}
	}
}

// normalizeCycle возвращает каноническое представление цикла без повторяющейся исходной валюты.
func normalizeCycle(cycle []string) string {
	n := len(cycle)
	if n == 0 {
		return ""
	}
	// Исключаем повторную исходную валюту (последний элемент), т.к. он совпадает с первым.
	cycleNoDup := cycle[:n-1]
	best := make([]string, len(cycleNoDup))
	copy(best, cycleNoDup)
	for i := 1; i < len(cycleNoDup); i++ {
		rotated := append(append([]string(nil), cycleNoDup[i:]...), cycleNoDup[:i]...)
		if lexLess(rotated, best) {
			best = rotated
		}
	}
	return strings.Join(best, "->")
}

// lexLess обеспечивает лексикографическое сравнение двух срезов строк.
func lexLess(a, b []string) bool {
	n := len(a)
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return true
		} else if a[i] > b[i] {
			return false
		}
	}
	return false
}

// RateRecord для парсинга входных данных.
type RateRecord struct {
	From     string  `json:"from"`
	To       string  `json:"to"`
	Rate     float64 `json:"rate"`
	Exchange string  `json:"exchange"`
}

// processFile открывает и анализирует файл с курсами.
func processFile(filename string) {
	f, err := os.Open(filename)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		return
	}
	defer f.Close()

	var records []RateRecord
	if err := json.NewDecoder(f).Decode(&records); err != nil {
		fmt.Fprintln(os.Stderr, "decode:", err)
		return
	}

	g := NewGraph()
	for _, r := range records {
		if r.Rate <= 0 {
			continue
		}
		g.AddEdge(r.From, r.To, r.Rate, r.Exchange)
	}

	fmt.Println("==== Анализ файла", filename, "в", time.Now(), "====")
	g.detectArbitrage()
}

func main() {
	var file string
	flag.StringVar(&file, "f", "", "JSON file with rate records (default stdin)")
	flag.Parse()

	if file != "" {
		// При указанном файле запускаем анализ каждые 5 секунд.
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			processFile(file)
			<-ticker.C
		}
	} else {
		// Если файл не указан, читаем данные из STDIN один раз.
		var records []RateRecord
		if err := json.NewDecoder(os.Stdin).Decode(&records); err != nil {
			fmt.Fprintln(os.Stderr, "decode:", err)
			os.Exit(1)
		}

		g := NewGraph()
		for _, r := range records {
			if r.Rate <= 0 {
				continue
			}
			g.AddEdge(r.From, r.To, r.Rate, r.Exchange)
		}
		g.detectArbitrage()
	}
}
