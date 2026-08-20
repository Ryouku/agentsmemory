package mcpserver

import (
	"context"
	"fmt"
	"strings"
)

// inboxRoom is the room the handoff convention files into. A session that finds
// a problem in another project files a finding here rather than editing a
// repository it has no context for.
const inboxRoom = "inbox"

// wingReader is the slice of the palace this check needs: whether a wing holds
// anything, and what the team's wings are called. Declared at the consumer so
// the decision can be driven by a test without a database behind it.
type wingReader interface {
	WingIsEmpty(ctx context.Context, teamID, wing string) (bool, error)
	WingNames(ctx context.Context, teamID string) ([]string, error)
}

// handoffRefusal returns the message to send back instead of filing, or "" to
// proceed.
//
// It catches one mistake: a handoff filed into a wing no session will ever look
// in. Measured 2026-08-20 on a 217-drawer palace, two sessions filed six drawers
// of real findings into wings named for the DIRECTION of travel —
// wing_to-<project> rather than wing_<project>. The resolution rungs an agent
// follows can only ever produce the latter, so both handoffs were undeliverable
// from the moment they were written, and neither filer had any way to notice.
//
// The discriminator is deliberately narrow: the wing holds NOTHING and the room
// is the inbox. "This wing is new" alone would be a false alarm on the case the
// protocol protects in three separate paragraphs — a wing comes into existence
// when something is first written to it, so on a fresh install every wing is
// missing and a warning would fire on correct behaviour. What separates the two
// is that nobody's first act in a palace is filing an inbox item to themselves.
// Checked before adopting: across all 217 drawers, every legitimate wing's first
// write was `decisions` or `diary`, and the only two whose first write was
// `inbox` are the two malformed ones. Zero false positives.
//
// It fails OPEN. If the palace cannot answer, the write proceeds: this guards a
// naming mistake, and turning a database hiccup into lost work would cost more
// than the mistake does.
func handoffRefusal(ctx context.Context, wings wingReader, teamID, wing, room string, confirmed bool) string {
	if confirmed || !strings.EqualFold(strings.TrimSpace(room), inboxRoom) {
		return ""
	}
	empty, err := wings.WingIsEmpty(ctx, teamID, wing)
	if err != nil || !empty {
		return ""
	}

	existing := "none yet — this is the first memory in this palace"
	if names, err := wings.WingNames(ctx, teamID); err == nil && len(names) > 0 {
		existing = strings.Join(names, ", ")
	}

	// Only suggest a correction when there is one. strippedDirection returns the
	// name unchanged for a wing that carries no direction prefix, which produced
	// "wing_alpha is almost always meant to be wing_alpha" — advice that reads as
	// a bug and teaches the reader to stop reading the message.
	suggestion := ""
	if fixed := strippedDirection(wing); fixed != wing {
		suggestion = fmt.Sprintf("%q is almost always meant to be %q. ", wing, fixed)
	}

	return fmt.Sprintf(
		"%q holds no memories, and you are filing an inbox item into it — a handoff nobody will "+
			"read. A target wing is named for the PROJECT, exactly as that project's own sessions "+
			"resolve it (its repository or directory name), never for the direction of travel. "+
			"%sWings that exist: %s. "+
			"If %q really is the project's wing and this is genuinely the first memory filed for "+
			"it, pass confirm_new_wing: true and this will file as sent.",
		wing, suggestion, existing, wing)
}

// strippedDirection is the correction to suggest: the wing minus a leading "to-"
// on the name after the wing_ prefix, which is the substitution both real
// failures made. When the name carries no such prefix the wing is returned
// unchanged, so the message degrades to "check this name" rather than inventing
// a different wrong one.
func strippedDirection(wing string) string {
	const p = "wing_"
	name, ok := strings.CutPrefix(wing, p)
	if !ok {
		return wing
	}
	if rest, cut := strings.CutPrefix(name, "to-"); cut && rest != "" {
		return p + rest
	}
	return wing
}
