import { StepRow } from "./StepRow.jsx";
import { CCSection } from "./CCSection.jsx";
import { QuestionCard } from "./QuestionCard.jsx";
import { ApproveButton } from "./ApproveButton.jsx";
import { JobFooter } from "./JobFooter.jsx";
import { slackURL } from "../state/job.js";

export function StepTimeline({ items }) {
  const elements = [];

  for (let i = 0; i < items.length; i++) {
    const item = items[i];

    switch (item.type) {
      case "cc-section": {
        // Collect all cc items belonging to this section.
        const ccItems = [];
        for (let j = i + 1; j < items.length; j++) {
          if (items[j].ccIdx === item.idx) {
            ccItems.push(items[j]);
          } else if (
            items[j].type === "cc-section" ||
            items[j].type === "step" ||
            items[j].type === "approve" ||
            items[j].type === "question" ||
            items[j].type === "footer"
          ) {
            break;
          }
        }
        elements.push(
          <CCSection key={"cc-" + item.idx} item={item} ccItems={ccItems} />
        );
        break;
      }
      case "step":
        elements.push(
          <StepRow
            key={"step-" + item.idx}
            toolName={item.toolName}
            desc={item.desc}
            status={item.status}
            duration={item.duration}
            prURL={item.prURL}
          />
        );
        break;
      case "question":
        elements.push(
          <QuestionCard
            key={"q-" + i}
            text={item.text}
            answered={item.answered}
            slackURL={slackURL.value}
          />
        );
        break;
      case "approve":
        elements.push(
          <ApproveButton
            key={"approve-" + i}
            status={item.status}
            approvedBy={item.approvedBy}
          />
        );
        break;
      case "footer":
        elements.push(
          <JobFooter key={"footer-" + i} isError={item.isError} data={item.data} />
        );
        break;
      default:
        // cc items (thinking, tool, text, etc.) are consumed by CCSection above.
        break;
    }
  }

  return <div class="step-list">{elements}</div>;
}
