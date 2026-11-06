import { Link } from "react-router";
import { Empty, Page } from "@/components/Page";

export function NotFoundPage() {
  return (
    <Page>
      <Empty title="No such page">
        <Link to="/" className="text-primary underline-offset-4 hover:underline">Back to the graph</Link>
      </Empty>
    </Page>
  );
}
