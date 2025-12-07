package eplb

// Adaptive EMA with Linear Regression
// 1분마다 업데이트 시 alpha를 linear regression으로 동적으로 계산

import (
	"math"
)

// ============================================================
// Linear Regression for Adaptive Alpha
// ============================================================

// LinearRegressionResult represents the result of linear regression
type LinearRegressionResult struct {
	Slope     float64 // 기울기 (변화율)
	Intercept float64 // y절편
	R2        float64 // 결정계수 (0~1, 1에 가까울수록 선형 관계 강함)
}

// ComputeLinearRegression computes simple linear regression: y = slope * x + intercept
// x: time indices (0, 1, 2, ..., n-1)
// y: QPS values
func ComputeLinearRegression(y []float64) LinearRegressionResult {
	n := len(y)
	if n < 2 {
		return LinearRegressionResult{
			Slope:     0,
			Intercept: y[0],
			R2:        0,
		}
	}

	// x는 시간 인덱스 (0, 1, 2, ..., n-1)
	sumX := float64(n * (n - 1) / 2) // 0+1+2+...+(n-1) = n*(n-1)/2
	sumY := 0.0
	sumXY := 0.0
	sumX2 := float64(n * (n - 1) * (2*n - 1) / 6) // 0²+1²+2²+...+(n-1)²
	sumY2 := 0.0

	for i := 0; i < n; i++ {
		x := float64(i)
		sumY += y[i]
		sumXY += x * y[i]
		sumY2 += y[i] * y[i]
	}

	meanX := sumX / float64(n)
	meanY := sumY / float64(n)

	// 기울기 계산: slope = (Σxy - n*meanX*meanY) / (Σx² - n*meanX²)
	denominator := sumX2 - float64(n)*meanX*meanX
	if math.Abs(denominator) < 1e-10 {
		return LinearRegressionResult{
			Slope:     0,
			Intercept: meanY,
			R2:        0,
		}
	}

	slope := (sumXY - float64(n)*meanX*meanY) / denominator
	intercept := meanY - slope*meanX

	// R² 계산 (결정계수)
	ssRes := 0.0 // Residual sum of squares
	ssTot := 0.0 // Total sum of squares
	for i := 0; i < n; i++ {
		predicted := slope*float64(i) + intercept
		ssRes += (y[i] - predicted) * (y[i] - predicted)
		ssTot += (y[i] - meanY) * (y[i] - meanY)
	}

	r2 := 0.0
	if ssTot > 1e-10 {
		r2 = 1.0 - ssRes/ssTot
	}

	return LinearRegressionResult{
		Slope:     slope,
		Intercept: intercept,
		R2:        r2,
	}
}

// ============================================================
// Adaptive Alpha Calculation
// ============================================================

// ComputeAdaptiveAlpha computes EMA alpha based on QPS trend
// Returns alpha value between MinAlpha and MaxAlpha
func ComputeAdaptiveAlpha(history []float64, minAlpha, maxAlpha float64) float64 {
	if len(history) < 2 {
		return (minAlpha + maxAlpha) / 2.0 // Default: middle value
	}

	// Linear regression으로 트렌드 분석
	regression := ComputeLinearRegression(history)

	// 기울기(slope)의 절댓값으로 변화율 측정
	absSlope := math.Abs(regression.Slope)
	meanValue := 0.0
	for _, v := range history {
		meanValue += v
	}
	meanValue /= float64(len(history))

	// 정규화된 변화율 (0~1)
	normalizedChangeRate := 0.0
	if meanValue > 1e-10 {
		normalizedChangeRate = absSlope / meanValue
	}

	// R²가 낮으면 (선형 관계가 약하면) 변화가 불규칙 → alpha를 낮춤
	// R²가 높고 변화율이 크면 → alpha를 높여서 빠르게 반응
	// R²가 높고 변화율이 작으면 → alpha를 낮춰서 안정적으로 유지

	// Alpha 계산 공식:
	// - 높은 R² + 높은 변화율 → 높은 alpha (빠른 적응)
	// - 낮은 R² 또는 낮은 변화율 → 낮은 alpha (안정적)
	alpha := minAlpha + (maxAlpha-minAlpha)*normalizedChangeRate*regression.R2

	// Clamp to valid range
	if alpha < minAlpha {
		alpha = minAlpha
	}
	if alpha > maxAlpha {
		alpha = maxAlpha
	}

	return alpha
}

// ============================================================
// EMA Update with Adaptive Alpha
// ============================================================

// UpdateEMAWithAdaptiveAlpha updates EMA using adaptive alpha based on history
// history: 최근 N분간의 QPS 값들 (최신이 앞에)
// currentQPS: 현재 QPS
// minAlpha, maxAlpha: alpha의 최소/최대값
func UpdateEMAWithAdaptiveAlpha(history []float64, currentQPS float64, minAlpha, maxAlpha float64) (float64, float64) {
	// History를 시간 순서대로 정렬 (오래된 것부터)
	reversedHistory := make([]float64, len(history))
	for i := 0; i < len(history); i++ {
		reversedHistory[i] = history[len(history)-1-i]
	}
	
	// 현재 QPS를 포함한 전체 시계열
	fullHistory := append(reversedHistory, currentQPS)

	// Adaptive alpha 계산
	adaptiveAlpha := ComputeAdaptiveAlpha(fullHistory, minAlpha, maxAlpha)

	// 기존 EMA 값 (history가 있으면 마지막 값, 없으면 currentQPS)
	oldEMA := currentQPS
	if len(history) > 0 {
		oldEMA = history[0] // 가장 최근 EMA 값
	}

	// EMA 업데이트
	newEMA := adaptiveAlpha*currentQPS + (1-adaptiveAlpha)*oldEMA

	return newEMA, adaptiveAlpha
}

