package resourcemap

import (
	"cmp"
	"slices"
)

type layoutPoint struct{ x, y float64 }

// Auto-layout only changes explicit positions. Inferred resources keep automatic
// operation grouping when materialized, just as with a manually dragged card.
func autoLayout(saved *stored, model Model) error {
	positions := resourcePositions(model)
	for _, resource := range model.Resources {
		if err := materialize(saved, model, resource.ID); err != nil {
			return err
		}
	}
	for i := range saved.Resources {
		point := positions[saved.Resources[i].ID]
		saved.Resources[i].X = point.x
		saved.Resources[i].Y = point.y
	}
	return nil
}

func resourcePositions(model Model) map[string]layoutPoint {
	resources := slices.Clone(model.Resources)
	slices.SortFunc(resources, func(a, b ResourceView) int {
		return cmp.Or(cmp.Compare(a.Service, b.Service), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	index := make(map[string]int, len(resources))
	for i, r := range resources {
		index[r.ID] = i
	}
	links := make([][]int, len(resources))
	neighbors := make([][]int, len(resources))
	for _, relation := range model.Relations {
		from, hasFrom := index[relation.FromResourceID]
		to, hasTo := index[relation.ToResourceID]
		if !hasFrom || !hasTo || from == to {
			continue
		}
		links[from] = append(links[from], to)
		neighbors[from] = append(neighbors[from], to)
		neighbors[to] = append(neighbors[to], from)
	}
	for i := range links {
		slices.Sort(links[i])
		links[i] = slices.Compact(links[i])
		slices.Sort(neighbors[i])
		neighbors[i] = slices.Compact(neighbors[i])
	}
	ranks := resourceRanks(links)
	positions := make(map[string]layoutPoint, len(resources))
	visited := make([]bool, len(resources))
	// Cards are 220px wide and at most 196px tall. Fixed gaps leave room for
	// padded labels; disconnected components pack into rows instead of a tall list.
	const columnStep, rowStep = 380, 280
	cursorX, cursorY, rowHeight := 40, 40, 0
	for first := range resources {
		if visited[first] {
			continue
		}
		component := []int{first}
		visited[first] = true
		for j := 0; j < len(component); j++ {
			for _, next := range neighbors[component[j]] {
				if !visited[next] {
					visited[next] = true
					component = append(component, next)
				}
			}
		}
		slices.Sort(component)
		columns := make(map[int][]int)
		lastColumn, mostRows := 0, 0
		for _, node := range component {
			rank := ranks[node]
			columns[rank] = append(columns[rank], node)
			lastColumn = max(lastColumn, rank)
			mostRows = max(mostRows, len(columns[rank]))
		}
		width := lastColumn*columnStep + 220
		height := (mostRows-1)*rowStep + 196
		if cursorX > 40 && cursorX+width > 1140 {
			cursorX = 40
			cursorY += rowHeight + 100
			rowHeight = 0
		}
		for column, nodes := range columns {
			for row, node := range nodes {
				positions[resources[node].ID] = layoutPoint{
					x: float64(cursorX + column*columnStep),
					y: float64(cursorY + row*rowStep),
				}
			}
		}
		cursorX += width + 160
		rowHeight = max(rowHeight, height)
	}
	return positions
}

// Collapse strongly connected components before assigning layers: a cycle is a
// vertical group, and every relation between groups advances left to right.
func resourceRanks(links [][]int) []int {
	groups, membership := resourceCycles(links)
	forward := make([][]int, len(groups))
	degree := make([]int, len(groups))
	for from, targets := range links {
		for _, to := range targets {
			a, b := membership[from], membership[to]
			if a != b {
				forward[a] = append(forward[a], b)
			}
		}
	}
	for i := range forward {
		slices.Sort(forward[i])
		forward[i] = slices.Compact(forward[i])
		for _, to := range forward[i] {
			degree[to]++
		}
	}
	queue := []int{}
	for i, count := range degree {
		if count == 0 {
			queue = append(queue, i)
		}
	}
	layers := make([]int, len(groups))
	for head := 0; head < len(queue); head++ {
		from := queue[head]
		for _, to := range forward[from] {
			layers[to] = max(layers[to], layers[from]+1)
			degree[to]--
			if degree[to] == 0 {
				queue = append(queue, to)
			}
		}
	}
	ranks := make([]int, len(links))
	for node, group := range membership {
		ranks[node] = layers[group]
	}
	return ranks
}

func resourceCycles(links [][]int) ([][]int, []int) {
	order := make([]int, len(links))
	low := make([]int, len(links))
	onStack := make([]bool, len(links))
	membership := make([]int, len(links))
	stack := []int{}
	groups := [][]int{}
	nextOrder := 1
	var visit func(int)
	visit = func(node int) {
		order[node], low[node] = nextOrder, nextOrder
		nextOrder++
		stack = append(stack, node)
		onStack[node] = true
		for _, target := range links[node] {
			if order[target] == 0 {
				visit(target)
				low[node] = min(low[node], low[target])
			} else if onStack[target] {
				low[node] = min(low[node], order[target])
			}
		}
		if low[node] != order[node] {
			return
		}
		group := []int{}
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			membership[last] = len(groups)
			group = append(group, last)
			if last == node {
				break
			}
		}
		groups = append(groups, group)
	}
	for node := range links {
		if order[node] == 0 {
			visit(node)
		}
	}
	return groups, membership
}
