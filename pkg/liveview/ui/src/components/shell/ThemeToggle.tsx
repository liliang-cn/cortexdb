import { Monitor, Moon, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useText } from "@/lib/i18n";
import { SHELL_TEXT } from "@/lib/shellStrings";
import { setMode, useMode, type Mode } from "@/lib/theme";

const ICON = { light: Sun, dark: Moon, auto: Monitor } as const;

export function ThemeToggle() {
  const mode = useMode();
  const Icon = ICON[mode];
  const t = useText(SHELL_TEXT);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={t.theme}>
          <Icon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-36">
        <DropdownMenuRadioGroup value={mode} onValueChange={(v) => setMode(v as Mode)}>
          <DropdownMenuRadioItem value="light"><Sun /> {t.light}</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark"><Moon /> {t.dark}</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="auto"><Monitor /> {t.auto}</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
