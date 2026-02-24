import "../styles/question.css";
import { renderMd } from "../lib/markdown.js";

export function QuestionCard({ text, answered, slackURL }) {
  return (
    <div class="cc-question">
      <div class="cc-question-label">Question</div>
      <div
        class="cc-question-body"
        dangerouslySetInnerHTML={{ __html: renderMd(text) }}
      />
      {!answered && slackURL && (
        <div class="cc-question-action">
          <a href={slackURL} target="_blank">
            Reply in Slack &#8599;
          </a>
        </div>
      )}
    </div>
  );
}
