import { Nav } from "./components/Nav.jsx";
import { OverviewPage } from "./pages/OverviewPage.jsx";
import { JobPage } from "./pages/JobPage.jsx";

export function App() {
  const path = window.location.pathname;
  const match = path.match(/^\/jobs\/([^/]+)/);

  return (
    <>
      <Nav />
      <div class="page">
        {match ? <JobPage id={match[1]} /> : <OverviewPage />}
      </div>
    </>
  );
}
