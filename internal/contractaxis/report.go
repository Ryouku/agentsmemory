package contractaxis

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// WriteReport writes every axis and residual in deterministic order.
func WriteReport(w io.Writer, report Report) error {
	if _, err := fmt.Fprintf(w, "CONTRACT AXES: %s\n", report.Status); err != nil {
		return err
	}
	axes := append([]AxisReport(nil), report.Axes...)
	sort.SliceStable(axes, func(i, j int) bool { return axes[i].Axis < axes[j].Axis })
	for _, axis := range axes {
		if _, err := fmt.Fprintf(w, "%s: %s maturity=%s universe=%d residuals=%d\n",
			axis.Axis, axis.Status, axis.Maturity, axis.Universe, len(axis.Residuals)); err != nil {
			return err
		}
		residuals := append([]Residual(nil), axis.Residuals...)
		sort.SliceStable(residuals, func(i, j int) bool {
			return lessResidualKey(residuals[i].Key, residuals[j].Key)
		})
		for _, residual := range residuals {
			marker := "OPEN"
			metadata := ""
			if residual.Excepted {
				marker = "EXCEPTED"
				metadata = exceptionMetadata(residual.Exception)
			} else if residual.Obligation != nil {
				marker = "RATCHET"
				metadata = ratchetMetadata(residual.Obligation)
			}
			if _, err := fmt.Fprintf(w, "  - %s %s: %s%s\n", marker, residual.Key.String(), residual.Detail, metadata); err != nil {
				return err
			}
		}
	}
	return nil
}

func exceptionMetadata(exception *Exception) string {
	if exception == nil {
		return ""
	}
	lifetime := "expires=" + exception.Expires.UTC().Format(time.RFC3339)
	if exception.Expires.IsZero() {
		lifetime = "permanent=" + exception.PermanentReason
	}
	return fmt.Sprintf(" [kind=%s owner=%s reference=%s %s reason=%s]",
		exception.Kind, exception.Owner, exception.Reference, lifetime, exception.Reason)
}

func ratchetMetadata(obligation *RatchetObligation) string {
	if obligation == nil {
		return ""
	}
	return fmt.Sprintf(" [owner=%s reference=%s expires=%s reason=%s]",
		obligation.Owner, obligation.Reference, obligation.Expires.UTC().Format(time.RFC3339), obligation.Reason)
}
