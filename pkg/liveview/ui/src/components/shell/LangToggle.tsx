import { Languages } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { isLang, LANGS, useLang, useSetLang } from "@/lib/i18n";
import { SHELL_TEXT } from "@/lib/shellStrings";

/** 中 or EN: the language the whole app speaks, kept for the next visit. */
const MARK = { en: "EN", zh: "中" } as const;

export function LangToggle() {
  const lang = useLang();
  const setLang = useSetLang();
  const t = SHELL_TEXT[lang];
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={`${t.language}: ${t.langName[lang]}`} title={t.language}>
          <span aria-hidden className="text-xs font-semibold">{MARK[lang]}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-36">
        <DropdownMenuRadioGroup value={lang} onValueChange={(v) => isLang(v) && setLang(v)}>
          {LANGS.map((l) => (
            <DropdownMenuRadioItem key={l} value={l} lang={l === "zh" ? "zh-CN" : "en"}>
              <Languages aria-hidden /> {t.langName[l]}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
