function xhs_studio_stop
    launchctl stop xhs-studio
end

function xhs_studio_start
    launchctl start xhs-studio
end

function xhs_studio_status
    launchctl list xhs-studio
end