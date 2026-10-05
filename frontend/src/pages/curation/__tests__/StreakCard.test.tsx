import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import StreakCard from "../StreakCard";

// These tests exist because the DESIGN constraint is the requirement here, and a
// design constraint cannot be verified by a compiler or a type checker. Each one
// is a place where an unremarkable-looking edit -- swapping "No streak yet" for
// "0 day streak", or moving totalActiveDays out of the headline -- would make the
// feature measure pressure instead of contribution, and still render perfectly.

describe("StreakCard", () => {
  it("reports a lifetime total that cannot decay", () => {
    // totalActiveDays is the headline: a user who stopped a year ago still sees
    // their record intact. If this were the streak, every card would decay.
    render(
      <StreakCard
        streak={{
          currentStreak: 0,
          longestStreak: 40,
          totalActiveDays: 213,
          activeToday: false,
          lastActiveDay: "2025-09-01",
        }}
      />,
    );

    expect(screen.getByText("213")).toBeInTheDocument();
    expect(screen.getByText(/active days, all time/i)).toBeInTheDocument();
  });

  it("reads a zero streak as 'No streak yet', never as a loss", () => {
    render(
      <StreakCard
        streak={{
          currentStreak: 0,
          longestStreak: 12,
          totalActiveDays: 30,
          activeToday: false,
          lastActiveDay: "2026-09-20",
        }}
      />,
    );

    // The word that must never appear on this card.
    expect(screen.queryByText(/lost/i)).toBeNull();
    expect(screen.queryByText(/you.ll lose/i)).toBeNull();
    expect(screen.getByText(/no streak yet/i)).toBeInTheDocument();
  });

  it("states when the streak last ended rather than leaving it at zero", () => {
    render(
      <StreakCard
        streak={{
          currentStreak: 0,
          longestStreak: 8,
          totalActiveDays: 22,
          activeToday: false,
          lastActiveDay: "2026-09-20",
        }}
      />,
    );

    // The honest version of a reset: a fact about when, not a punishment.
    expect(screen.getByText(/last active 2026-09-20/i)).toBeInTheDocument();
  });

  it("does not imply today is done when the streak is merely alive", () => {
    // currentStreak > 0 with activeToday false is the state this card exists to
    // render honestly: yesterday's activity keeps the run, but today has not
    // happened yet. Collapsing these would imply a day that is not finished.
    render(
      <StreakCard
        streak={{
          currentStreak: 5,
          longestStreak: 9,
          totalActiveDays: 61,
          activeToday: false,
          lastActiveDay: "2026-09-30",
        }}
      />,
    );

    expect(screen.queryByText(/active today/i)).toBeNull();
    expect(screen.getByText(/last active 2026-09-30/i)).toBeInTheDocument();
  });

  it("marks today active only when the backend says so", () => {
    render(
      <StreakCard
        streak={{
          currentStreak: 5,
          longestStreak: 9,
          totalActiveDays: 61,
          activeToday: true,
          lastActiveDay: "2026-10-01",
        }}
      />,
    );

    expect(screen.getByText(/active today/i)).toBeInTheDocument();
  });

  it("invites a first contribution when there is no history at all", () => {
    render(
      <StreakCard
        streak={{
          currentStreak: 0,
          longestStreak: 0,
          totalActiveDays: 0,
          activeToday: false,
          lastActiveDay: null,
        }}
      />,
    );

    // lastActiveDay is null here, so the card must not render a date -- not
    // today, not the epoch. "Last active 1970-01-01" would be worse than silence.
    expect(screen.queryByText(/1970/)).toBeNull();
    expect(screen.getByText(/no contributions yet/i)).toBeInTheDocument();
  });
});
