import { Link } from "react-router";
import { Empty, Page } from "@/components/Page";
import { useText } from "@/lib/i18n";
import { SHELL_TEXT } from "@/lib/shellStrings";

export function NotFoundPage() {
  const t = useText(SHELL_TEXT);
  return (
    <Page>
      <Empty title={t.notFound}>
        <Link to="/" className="text-primary underline-offset-4 hover:underline">{t.backToGraph}</Link>
      </Empty>
    </Page>
  );
}
