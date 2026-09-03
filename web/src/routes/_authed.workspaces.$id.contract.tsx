import { createFileRoute } from "@tanstack/react-router";
import { ContractPage } from "@/components/ContractPage";

// P7b (DESIGN §34.5): the tenth tab, «Контракт» — read-only, the A4 rule
// lifted for it by the owner («снимаю ограничения для 3 пункта»).
export const Route = createFileRoute("/_authed/workspaces/$id/contract")({
  component: ContractRoute,
});

function ContractRoute() {
  const { id } = Route.useParams();
  return <ContractPage id={id} />;
}
