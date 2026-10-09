import { kindName, nodeSubtitle, type MapNode } from "./model";
import styles from "./Explorer.module.css";

export const exploreCardWidth = 248;
export const exploreCardMinHeight = 146;

export function createExploreCard(data: MapNode & { selected?: boolean }) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = styles.mapCard ?? "";
  button.dataset.objectId = data.id;
  button.dataset.kind = data.kind;
  if (data.change) button.dataset.change = data.change;
  button.dataset.selected = String(!!data.selected);
  button.setAttribute("aria-pressed", String(!!data.selected));
  const type = document.createElement("span");
  type.className = styles.cardType ?? "";
  const icon = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  icon.setAttribute("viewBox", "0 0 24 24");
  icon.setAttribute("width", "14");
  icon.setAttribute("height", "14");
  icon.setAttribute("fill", "none");
  icon.setAttribute("stroke", "currentColor");
  icon.setAttribute("stroke-width", "1.6");
  icon.setAttribute("aria-hidden", "true");
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute(
    "d",
    ["datastore", "data_store", "table"].includes(data.kind)
      ? "M4 6c0-4 16-4 16 0v12c0 4-16 4-16 0V6Zm0 0c0 4 16 4 16 0M4 12c0 4 16 4 16 0"
      : data.kind === "external_system"
        ? "M13 5h6v6m0-6L9 15M9 5H5v14h14v-4"
        : "M4 4h16v16H4zM4 9h16M9 9v11",
  );
  icon.append(path);
  type.append(icon, document.createTextNode(kindName(data.kind)));
  const title = document.createElement("strong");
  title.className = styles.cardTitle ?? "";
  title.textContent = data.name;
  const description = document.createElement("span");
  description.className = styles.cardDescription ?? "";
  description.textContent = data.description === data.name ? "" : data.description;
  const foot = document.createElement("span");
  foot.className = styles.cardFoot ?? "";
  foot.textContent =
    data.kind === "query" ? nodeSubtitle(data) : (data.badge ?? nodeSubtitle(data) ?? "");
  if (data.kind === "query") foot.title = data.id;
  button.append(type, title, description, foot);
  return button;
}
