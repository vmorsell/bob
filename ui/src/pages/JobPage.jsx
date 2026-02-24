import { useEffect, useRef } from "preact/hooks";
import { fetchJobEvents, tokenQueryParam } from "../lib/api.js";
import { addEvt, resetEventState } from "../lib/events.js";
import {
  autoScroll,
  prLink,
  taskText,
  slackURL,
  jobCostUSD,
  isLive,
  currentPhase,
  items,
  resetJobState,
} from "../state/job.js";
import { JobHeader } from "../components/JobHeader.jsx";
import { StepTimeline } from "../components/StepTimeline.jsx";
import { useAutoScroll, scrollToBottomIfAuto } from "../hooks/useAutoScroll.js";
import "../styles/job.css";

export function JobPage({ id }) {
  const esRef = useRef(null);
  const prevLenRef = useRef(0);

  useAutoScroll();

  useEffect(() => {
    document.title = "Job " + id.slice(0, 8) + " \u2013 Bob";
    resetJobState();
    resetEventState();

    fetchJobEvents(id)
      .then((evts) => {
        (evts || []).forEach(addEvt);

        let evtURL = "/events?job=" + encodeURIComponent(id);
        const tqp = tokenQueryParam();
        if (tqp) evtURL += "&" + tqp;
        const es = new EventSource(evtURL);
        esRef.current = es;
        isLive.value = true;

        es.onmessage = (e) => {
          addEvt(JSON.parse(e.data));
          scrollToBottomIfAuto();
        };
        es.onerror = () => {
          isLive.value = false;
        };
      })
      .catch(() => {
        // Set an error state that renders "Job not found".
        taskText.value = "";
        isLive.value = false;
      });

    return () => {
      if (esRef.current) {
        esRef.current.close();
        esRef.current = null;
      }
    };
  }, [id]);

  // Scroll to bottom when items are added (from history replay).
  useEffect(() => {
    if (items.value.length > prevLenRef.current) {
      scrollToBottomIfAuto();
    }
    prevLenRef.current = items.value.length;
  }, [items.value]);

  if (!taskText.value && !isLive.value && items.value.length === 0) {
    return <div class="placeholder">Job not found.</div>;
  }

  return (
    <>
      <JobHeader
        taskText={taskText.value}
        slackURL={slackURL.value}
        prLink={prLink.value}
        jobCostUSD={jobCostUSD.value}
        currentPhase={currentPhase.value}
        isLive={isLive.value}
      />
      <StepTimeline items={items.value} />
      {prLink.value && (
        <a
          href={prLink.value}
          target="_blank"
          style="display:inline-flex;align-items:center;gap:6px;margin-top:24px;font-size:14px;color:var(--text-secondary)"
        >
          View Pull Request on GitHub &#8599;
        </a>
      )}
    </>
  );
}
