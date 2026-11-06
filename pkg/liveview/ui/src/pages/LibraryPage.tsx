import { lazy, Suspense } from "react";
import { Loader2 } from "lucide-react";
import { useLang, type Strings } from "@/lib/i18n";

// three.js is most of the library's weight, so the reading room loads on its
// own and the other pages never pay for it.
const LibraryView = lazy(() => import("@/components/library/LibraryView"));

const LOADING: Strings<string> = { en: "Opening the library", zh: "正在打开图书馆" };

/** The brain as a reading room: every memory a book on its project's shelf. */
export function LibraryPage() {
  const lang = useLang();
  return (
    <Suspense
      fallback={
        <div className="flex h-full items-center justify-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          {LOADING[lang]}
        </div>
      }
    >
      <LibraryView />
    </Suspense>
  );
}
