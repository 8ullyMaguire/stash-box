import type { FC } from "react";
import { Route, Routes } from "react-router-dom";

import {
  ROUTE_ACTIVATE,
  ROUTE_ARCHIVE,
  ROUTE_AUDITS,
  ROUTE_CATEGORIES,
  ROUTE_CURATION,
  ROUTE_DRAFTS,
  ROUTE_EDITS,
  ROUTE_ELO_LEADERBOARD,
  ROUTE_FORGOT_PASSWORD,
  ROUTE_HOME,
  ROUTE_IDENTIFICATION,
  ROUTE_IMAGE_REVIEW,
  ROUTE_IMAGE_TYPES,
  ROUTE_LISTS,
  ROUTE_LOGIN,
  ROUTE_NOTIFICATIONS,
  ROUTE_PERFORMERS,
  ROUTE_REGISTER,
  ROUTE_RESET_PASSWORD,
  ROUTE_SCENES,
  ROUTE_SEARCH,
  ROUTE_SITE_CATEGORIES,
  ROUTE_SITES,
  ROUTE_STUDIOS,
  ROUTE_TAGS,
  ROUTE_USERS,
  ROUTE_VERSION,
} from "src/constants/route";
import Login from "src/Login";
import ActivateUser from "src/pages/activateUser";
import ArchiveStatePage from "src/pages/archive";
import Audits from "src/pages/audits";
import Categories from "src/pages/categories";
import Curation from "src/pages/curation";
import Drafts from "src/pages/drafts";
import Edits from "src/pages/edits";
import ForgotPassword from "src/pages/forgotPassword";
import Home from "src/pages/home";
import Identification from "src/pages/identification";
import ImageReview from "src/pages/imageReview";
import ImageTypes from "src/pages/imageTypes";
import EloLeaderboard from "src/pages/leaderboard";
import Notifications from "src/pages/notifications";
import Performers from "src/pages/performers";
import RegisterUser from "src/pages/registerUser";
import ResetPassword from "src/pages/resetPassword";
import Scenes from "src/pages/scenes";
import Search from "src/pages/search";
import Lists from "src/pages/lists";
import SiteCategories from "src/pages/siteCategories";
import Sites from "src/pages/sites";
import Studios from "src/pages/studios";
import Tags from "src/pages/tags";
import Users from "src/pages/users";
import Version from "src/pages/version";

const Pages: FC = () => (
  <Routes>
    <Route path={ROUTE_HOME} element={<Home />} />
    <Route
      path="/*"
      element={
        <div className="NarrowPage">
          <Routes>
            <Route path={ROUTE_LOGIN} element={<Login />} />
            <Route path={`${ROUTE_USERS}/*`} element={<Users />} />
            <Route path={`${ROUTE_PERFORMERS}/*`} element={<Performers />} />
            <Route path={`${ROUTE_SCENES}/*`} element={<Scenes />} />
            <Route path={`${ROUTE_STUDIOS}/*`} element={<Studios />} />
            <Route path={`${ROUTE_TAGS}/*`} element={<Tags />} />
            <Route path={`${ROUTE_LISTS}/*`} element={<Lists />} />
            <Route path={`${ROUTE_EDITS}/*`} element={<Edits />} />
            <Route path={`${ROUTE_CATEGORIES}/*`} element={<Categories />} />
            <Route path={ROUTE_REGISTER} element={<RegisterUser />} />
            <Route path={ROUTE_ACTIVATE} element={<ActivateUser />} />
            <Route path={ROUTE_FORGOT_PASSWORD} element={<ForgotPassword />} />
            <Route path={ROUTE_RESET_PASSWORD} element={<ResetPassword />} />
            <Route path={`${ROUTE_SEARCH}/*`} element={<Search />} />
            <Route path={ROUTE_VERSION} element={<Version />} />
            <Route path={`${ROUTE_SITES}/*`} element={<Sites />} />
            <Route
              path={`${ROUTE_SITE_CATEGORIES}/*`}
              element={<SiteCategories />}
            />
            <Route path={ROUTE_IMAGE_TYPES} element={<ImageTypes />} />
            <Route path={`${ROUTE_IMAGE_REVIEW}/*`} element={<ImageReview />} />
            {/* Identification board. Mounted at the top level, NOT under
                /curation: the board reads at READ while /curation is gated on
                VOTE, so nesting it there would hide it from read-only users. */}
            <Route
              path={`${ROUTE_IDENTIFICATION}/*`}
              element={<Identification />}
            />
            <Route
              path={`${ROUTE_ELO_LEADERBOARD}/*`}
              element={<EloLeaderboard />}
            />
            <Route path={ROUTE_ARCHIVE} element={<ArchiveStatePage />} />
            <Route path={`${ROUTE_CURATION}/*`} element={<Curation />} />
            <Route path={`${ROUTE_DRAFTS}/*`} element={<Drafts />} />
            <Route path={ROUTE_NOTIFICATIONS} element={<Notifications />} />
            <Route path={`${ROUTE_AUDITS}/*`} element={<Audits />} />
          </Routes>
        </div>
      }
    />
  </Routes>
);

export default Pages;
