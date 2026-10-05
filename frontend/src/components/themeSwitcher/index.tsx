import type { FC } from "react";
import { Button, ButtonGroup } from "react-bootstrap";
import { useTheme } from "src/hooks/useTheme";

const LABELS: Record<string, string> = {
  system: "Auto",
  light: "Light",
  dark: "Dark",
};

/**
 * The theme switcher (growth item 20).
 *
 * A ButtonGroup of the three choices rather than a two-state toggle, because "system" is a
 * real option that a toggle can never select once you have left it -- and "follow the OS"
 * is the option most people want as their default.
 *
 * `aria-pressed` marks the ACTIVE choice, so a screen reader announces which palette is in
 * effect rather than leaving three equally-labelled buttons.
 */
const ThemeSwitcher: FC = () => {
  const { choice, setChoice } = useTheme();

  return (
    <ButtonGroup size="sm" role="group" aria-label="Colour theme">
      {(["system", "light", "dark"] as const).map((option) => (
        <Button
          key={option}
          variant={choice === option ? "primary" : "outline-secondary"}
          aria-pressed={choice === option}
          onClick={() => setChoice(option)}
        >
          {LABELS[option]}
        </Button>
      ))}
    </ButtonGroup>
  );
};

export default ThemeSwitcher;