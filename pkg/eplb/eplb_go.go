package eplb

// EPLB (Expert Parallelism Load Balancer) - Go Implementation
// DeepSeek 원본 알고리즘을 Go로 포팅
// Python PyTorch 의존성 없이 순수 Go로 구현

import (
	"fmt"
	"math/rand/v2"
	"sort"
)

// ============================================================
// EPLB Core Algorithm
// ============================================================

// BalancedPacking packs n weighted objects to m packs
// Returns: packIndex, rankInPack (both are []int, length = n)
func BalancedPacking(weights []float64, numPacks int) ([]int, []int) {
	n := len(weights)
	if n%numPacks != 0 {
		panic(fmt.Sprintf("n (%d) must be divisible by numPacks (%d)", n, numPacks))
	}

	groupsPerPack := n / numPacks

	if groupsPerPack == 1 {
		packIndex := make([]int, n)
		rankInPack := make([]int, n)
		for i := 0; i < n; i++ {
			packIndex[i] = i
			rankInPack[i] = 0
		}
		return packIndex, rankInPack
	}

	// Sort indices by weight (descending)
	type weightIndex struct {
		weight float64
		index  int
	}
	weighted := make([]weightIndex, n)
	for i, w := range weights {
		weighted[i] = weightIndex{weight: w, index: i}
	}
	sort.Slice(weighted, func(i, j int) bool {
		return weighted[i].weight > weighted[j].weight
	})

	packIndex := make([]int, n)
	rankInPack := make([]int, n)
	packWeights := make([]float64, numPacks)
	packItems := make([]int, numPacks)

	for _, wi := range weighted {
		// Find pack with minimum weight that has space
		// BUG FIX: minPack을 -1로 초기화하고, 유효한 pack만 선택
		minPack := -1
		minWeight := 0.0

		for p := 0; p < numPacks; p++ {
			// Skip full packs
			if packItems[p] >= groupsPerPack {
				continue
			}

			// First available pack or pack with lower weight
			if minPack == -1 || packWeights[p] < minWeight {
				minPack = p
				minWeight = packWeights[p]
			}
		}

		if minPack == -1 {
			panic("no available pack found (logic error)")
		}

		packIndex[wi.index] = minPack
		rankInPack[wi.index] = packItems[minPack]
		packWeights[minPack] += wi.weight
		packItems[minPack]++
	}

	return packIndex, rankInPack
}

// ReplicateExperts replicates experts to minimize max load
// Returns: phy2log, rank, logcnt
func ReplicateExperts(weights []float64, numPhy int) ([]int, []int, []int) {
	numLog := len(weights)
	numRedundant := numPhy - numLog
	if numRedundant < 0 {
		panic(fmt.Sprintf("numPhy (%d) must be >= numLog (%d)", numPhy, numLog))
	}

	phy2log := make([]int, numPhy)
	rank := make([]int, numPhy)
	logcnt := make([]int, numLog)

	// Initialize: each logical expert gets one physical replica
	for i := 0; i < numLog; i++ {
		phy2log[i] = i
		rank[i] = 0
		logcnt[i] = 1
	}

	// Add redundant replicas
	for i := numLog; i < numPhy; i++ {
		// Find expert with maximum load/current_replicas
		maxIdx := 0
		maxLoad := weights[0] / float64(logcnt[0])
		for j := 1; j < numLog; j++ {
			load := weights[j] / float64(logcnt[j])
			if load > maxLoad {
				maxIdx = j
				maxLoad = load
			}
		}

		phy2log[i] = maxIdx
		rank[i] = logcnt[maxIdx]
		logcnt[maxIdx]++
	}

	return phy2log, rank, logcnt
}

// RebalanceExpertsHierarchical performs hierarchical expert rebalancing
func RebalanceExpertsHierarchical(weights []float64, numPhysicalExperts, numGroups, numNodes, numGpus int) ([]int, []int, []int) {
	numLogicalExperts := len(weights)

	if numLogicalExperts%numGroups != 0 {
		panic(fmt.Sprintf("numLogicalExperts (%d) must be divisible by numGroups (%d)", numLogicalExperts, numGroups))
	}
	groupSize := numLogicalExperts / numGroups

	if numGroups%numNodes != 0 {
		panic(fmt.Sprintf("numGroups (%d) must be divisible by numNodes (%d)", numGroups, numNodes))
	}
	_ = numGroups / numNodes // groupsPerNode (not used in simplified version)

	if numGpus%numNodes != 0 {
		panic(fmt.Sprintf("numGpus (%d) must be divisible by numNodes (%d)", numGpus, numNodes))
	}

	if numPhysicalExperts%numGpus != 0 {
		panic(fmt.Sprintf("numPhysicalExperts (%d) must be divisible by numGpus (%d)", numPhysicalExperts, numGpus))
	}
	_ = numPhysicalExperts / numGpus // phyExpertsPerGpu (not used in simplified version)

	// Step 1: Pack groups to nodes (simplified - not fully used in current implementation)
	tokensPerGroup := make([]float64, numGroups)
	for g := 0; g < numGroups; g++ {
		sum := 0.0
		for i := 0; i < groupSize; i++ {
			sum += weights[g*groupSize+i]
		}
		tokensPerGroup[g] = sum
	}

	_, _ = BalancedPacking(tokensPerGroup, numNodes) // groupPackIndex, groupRankInPack (not used in simplified version)

	// Step 2: Construct redundant experts within nodes
	// Simplified: use global replication for now
	// (Full hierarchical implementation would be more complex)
	logcnt := make([]int, numLogicalExperts)
	phy2log := make([]int, numPhysicalExperts)
	rank := make([]int, numPhysicalExperts)

	// Use simple replication for each node's experts
	expertsPerNode := numLogicalExperts / numNodes
	phyPerNode := numPhysicalExperts / numNodes

	for node := 0; node < numNodes; node++ {
		nodeWeights := make([]float64, expertsPerNode)
		for i := 0; i < expertsPerNode; i++ {
			// Map logical expert to node (simplified)
			logIdx := node*expertsPerNode + i
			if logIdx < numLogicalExperts {
				nodeWeights[i] = weights[logIdx]
			}
		}

		nodePhy2log, nodeRank, nodeLogcnt := ReplicateExperts(nodeWeights, phyPerNode)

		// Map physical replicas to global logical indices
		for i := 0; i < phyPerNode; i++ {
			globalPhyIdx := node*phyPerNode + i
			localLogIdx := nodePhy2log[i] // 0..expertsPerNode-1
			globalLogIdx := node*expertsPerNode + localLogIdx
			phy2log[globalPhyIdx] = globalLogIdx
			rank[globalPhyIdx] = nodeRank[i]
		}

		// Map logical expert replica counts to global logcnt
		// BUG FIX: nodeLogcnt는 논리 expert 기준이므로, localLogIdx로 접근해야 함
		for localLogIdx, cnt := range nodeLogcnt {
			globalLogIdx := node*expertsPerNode + localLogIdx
			if globalLogIdx < numLogicalExperts {
				logcnt[globalLogIdx] = cnt
			}
		}
	}

	return phy2log, rank, logcnt
}

// RebalanceExperts is the main entry point for EPLB
func RebalanceExperts(weights []float64, numReplicas, numGroups, numNodes, numGpus int) []int {
	if len(weights) == 0 {
		return []int{}
	}

	// Simplified: use global replication if constraints not met
	if numGroups%numNodes != 0 {
		numGroups = 1
		numNodes = 1
	}

	_, _, logcnt := RebalanceExpertsHierarchical(weights, numReplicas, numGroups, numNodes, numGpus)
	return logcnt
}

// ============================================================
// EPLB Engine (High-level API)
// ============================================================

// ComputeExpertAssignment computes replica assignment using EPLB
// This is the main function to call from your application
func ComputeExpertAssignment(weights []float64, numReplicas int) []int {
	if len(weights) == 0 {
		return []int{}
	}

	numExperts := len(weights)

	// If numReplicas < numExperts, assign to top-weighted experts
	if numReplicas < numExperts {
		type expertWeight struct {
			weight float64
			index  int
		}
		ew := make([]expertWeight, numExperts)
		for i, w := range weights {
			ew[i] = expertWeight{weight: w, index: i}
		}
		sort.Slice(ew, func(i, j int) bool {
			return ew[i].weight > ew[j].weight
		})

		result := make([]int, numExperts)
		for i := 0; i < numReplicas; i++ {
			result[ew[i].index] = 1
		}
		return result
	}

	// Check if all weights are zero
	totalWeight := 0.0
	for _, w := range weights {
		totalWeight += w
	}

	if totalWeight == 0 {
		// Equal distribution
		base := numReplicas / numExperts
		remainder := numReplicas % numExperts
		result := make([]int, numExperts)
		for i := 0; i < numExperts; i++ {
			result[i] = base
			if i < remainder {
				result[i]++
			}
		}
		return result
	}

	// Use EPLB algorithm
	// Simplified: use numGroups=1, numNodes=1, numGpus=numReplicas
	logcnt := RebalanceExperts(weights, numReplicas, 1, 1, numReplicas)
	return logcnt
}

// ============================================================
// Fallback Proportional Assignment
// ============================================================

// FallbackProportionalAssignment assigns replicas proportionally to weights
func FallbackProportionalAssignment(weights []float64, numReplicas int) []int {
	totalWeight := 0.0
	for _, w := range weights {
		totalWeight += w
	}

	if totalWeight == 0 {
		base := numReplicas / len(weights)
		remainder := numReplicas % len(weights)
		result := make([]int, len(weights))
		for i := range result {
			result[i] = base
			if i < remainder {
				result[i]++
			}
		}
		return result
	}

	result := make([]int, len(weights))
	remaining := numReplicas

	for i, w := range weights {
		if i == len(weights)-1 {
			result[i] = remaining
		} else {
			share := int(float64(numReplicas) * (w / totalWeight))
			if share < 0 {
				share = 0
			}
			if share > remaining {
				share = remaining
			}
			result[i] = share
			remaining -= share
		}
	}

	return result
}

func GenerateRandomQPS(min, max float64) float64 {
	return min + rand.Float64()*(max-min)
}
