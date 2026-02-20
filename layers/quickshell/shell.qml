import Quickshell
import Quickshell.Wayland
import QtQuick
import QtQuick.Layouts

ShellRoot {
    PanelWindow {
        anchors {
            top: true
            left: true
            right: true
        }

        height: 32
        color: "#1e1e2e"

        WlrLayershell.namespace: "quickshell"
        WlrLayershell.layer: WlrLayer.Top

        RowLayout {
            anchors.fill: parent
            anchors.leftMargin: 8
            anchors.rightMargin: 8
            spacing: 8

            Text {
                text: "Overthink"
                color: "#cdd6f4"
                font.pixelSize: 14
                font.bold: true
            }

            Item { Layout.fillWidth: true }

            Text {
                id: clock
                color: "#cdd6f4"
                font.pixelSize: 14

                function updateTime() {
                    text = new Date().toLocaleTimeString(Qt.locale(), "HH:mm:ss")
                }

                Timer {
                    interval: 1000
                    running: true
                    repeat: true
                    triggeredOnStart: true
                    onTriggered: clock.updateTime()
                }
            }
        }
    }
}
