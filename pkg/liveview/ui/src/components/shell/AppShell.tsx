import { Outlet } from "react-router";
import { TooltipProvider } from "@/components/ui/tooltip";
import { LiveConnection } from "@/live";
import { LangProvider } from "@/lib/i18n";
import { CommandPalette } from "./CommandPalette";
import { TopBar } from "./TopBar";

/**
 * The frame every page sits in. The live stream is mounted here, above the
 * outlet, so route changes never touch it; so is the language every page
 * speaks (lib/i18n), so one choice holds for all of them.
 */
export function AppShell() {
  return (
    <LangProvider>
      <TooltipProvider delayDuration={200}>
        <LiveConnection />
        <CommandPalette />
        <div className="flex h-dvh flex-col">
          <TopBar />
          {/* relative: the scroller is the containing block of what is absolutely
              positioned inside it (every sr-only label is), or those escape it and
              stretch the document into a second, empty scroll below the app. */}
          <main className="relative min-h-0 flex-1 overflow-auto">
            <Outlet />
          </main>
        </div>
      </TooltipProvider>
    </LangProvider>
  );
}
